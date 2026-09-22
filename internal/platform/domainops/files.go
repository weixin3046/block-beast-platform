package domainops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) > 8<<20 {
		return fmt.Errorf("配置过大")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(dst); err != nil {
		return fmt.Errorf("JSON 配置格式错误: %s", path)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return fmt.Errorf("JSON 配置包含额外内容: %s", path)
	}
	return nil
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".domain-next-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(path, append(b, '\n'), 0600)
}

type snapshot struct {
	Path     string
	Data     []byte
	Mode     uint32
	UID, GID int
	Exists   bool
}

func capture(path string) (snapshot, error) {
	s := snapshot{Path: path}
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return s, e
	}
	if !info.Mode().IsRegular() {
		return s, fmt.Errorf("拒绝替换非普通文件: %s", path)
	}
	s.Exists = true
	s.Mode = uint32(info.Mode().Perm())
	st := info.Sys().(*syscall.Stat_t)
	s.UID = int(st.Uid)
	s.GID = int(st.Gid)
	s.Data, e = os.ReadFile(path)
	return s, e
}
func restore(s snapshot) error {
	if !s.Exists {
		err := os.Remove(s.Path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := atomicWrite(s.Path, s.Data, os.FileMode(s.Mode)); err != nil {
		return err
	}
	return os.Chown(s.Path, s.UID, s.GID)
}

// configGraph resolves only active includes, including globs. Backup files are
// not inspected unless an active include explicitly selects them.
func (e *Engine) configGraph() (map[string][]byte, error) {
	files := map[string][]byte{}
	active := map[string]bool{}
	var visit func(string) error
	visit = func(path string) error {
		if active[path] {
			return fmt.Errorf("Nginx include 循环")
		}
		if _, ok := files[path]; ok {
			return nil
		}
		active[path] = true
		defer delete(active, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ds, err := parseNginx(b)
		if err != nil {
			return err
		}
		files[path] = b
		return walkDirectives(ds, func(d directive) error {
			if d.words[0] != "include" {
				return nil
			}
			matches, err := e.includeMatches(d)
			if err != nil {
				return err
			}
			for _, p := range matches {
				if err = visit(p); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := visit(e.path(nginxMain)); err != nil {
		return nil, err
	}
	return files, nil
}
func (e *Engine) includeMatches(d directive) ([]string, error) {
	if len(d.words) != 2 || strings.ContainsAny(d.words[1], "$\n\r") {
		return nil, fmt.Errorf("不能解析 Nginx include")
	}
	p := d.words[1]
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(nginxMain), p)
	}
	matches, err := filepath.Glob(e.path(p))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 && !strings.ContainsAny(p, "*?[") {
		return nil, fmt.Errorf("include 文件不存在: %s", p)
	}
	return matches, nil
}
func (e *Engine) checkConflicts(graph map[string][]byte, s State, r Request) error {
	for path, b := range graph {
		ds, err := parseNginx(b)
		if err != nil {
			return err
		}
		err = walkDirectives(ds, func(d directive) error {
			if d.words[0] != "server_name" {
				return nil
			}
			for _, n := range d.words[1:] {
				if strings.EqualFold(n, r.Domain) {
					if path == e.configPath(r.Domain) {
						if _, ok := s.Domains[r.Domain]; ok {
							return nil
						}
					}
					if r.Operation == "import" && path == e.path(r.Source) {
						return nil
					}
					return fmt.Errorf("域名已在其他活动配置中: %s", path)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	// Own files are immutable from the tool's perspective; reject manual edits.
	for _, d := range s.Domains {
		want, err := RenderNginx(d)
		if err != nil {
			return err
		}
		actual, err := os.ReadFile(e.configPath(d.Name))
		if err != nil || !bytes.Equal(want, actual) {
			return fmt.Errorf("受管配置被外部修改: %s", d.Name)
		}
	}
	return nil
}

// shadowConfig expands includes to concrete staged copies. This preserves the
// whole server configuration while excluding removed files and adding new ones.
func (e *Engine) shadowConfig(dir string, graph map[string][]byte, changes map[string][]byte) (string, error) {
	files := map[string][]byte{}
	for p, b := range graph {
		files[p] = b
	}
	for p, b := range changes {
		if strings.HasSuffix(p, ".conf") {
			if b == nil {
				delete(files, p)
			} else {
				files[p] = b
			}
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	staged := map[string]string{}
	for i, p := range paths {
		staged[p] = filepath.Join(dir, fmt.Sprintf("config-%d.conf", i))
	}
	for _, p := range paths {
		b := files[p]
		ds, err := parseNginx(b)
		if err != nil {
			return "", err
		}
		type edit struct {
			a, z int
			v    string
		}
		var edits []edit
		err = walkDirectives(ds, func(d directive) error {
			if d.words[0] != "include" {
				return nil
			}
			if len(d.words) != 2 {
				return fmt.Errorf("include 无效")
			}
			pattern := d.words[1]
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(filepath.Dir(nginxMain), pattern)
			}
			pattern = e.path(pattern)
			var lines []string
			for _, candidate := range paths {
				ok, err := filepath.Match(pattern, candidate)
				if err != nil {
					return err
				}
				if ok {
					lines = append(lines, "include "+staged[candidate]+";")
				}
			}
			edits = append(edits, edit{d.start, d.end, strings.Join(lines, "\n")})
			return nil
		})
		if err != nil {
			return "", err
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].a > edits[j].a })
		for _, x := range edits {
			b = append(append(append([]byte(nil), b[:x.a]...), []byte(x.v)...), b[x.z:]...)
		}
		if err = atomicWrite(staged[p], b, 0600); err != nil {
			return "", err
		}
	}
	return staged[e.path(nginxMain)], nil
}
