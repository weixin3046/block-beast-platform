package domainops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	nginxMain        = "/www/server/nginx/conf/nginx.conf"
	vhostDir         = "/www/server/panel/vhost/nginx"
	stateDir         = "/etc/block-beast/domains"
	statePath        = stateDir + "/state.json"
	journalPath      = stateDir + "/pending.json"
	originsPath      = "/etc/block-beast/managed-origins.json"
	envPath          = "/etc/block-beast/block-beast.env"
	capabilityPath   = "/opt/block-beast/current/domain-management-v1"
	lockPath         = "/run/lock/block-beast-deploy.lock"
	nginxBinary      = "/www/server/nginx/sbin/nginx"
	supervisorBinary = "/www/server/panel/pyenv/bin/supervisorctl"
)

type Runner interface {
	Run(context.Context, string, ...string) error
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s 执行失败: %w", filepath.Base(name), err)
	}
	return nil
}

type Engine struct {
	Root   string
	Runner Runner
	Probe  func(context.Context, Domain) (ProbeResult, error)
	Roots  *x509.CertPool
}

func NewEngine(root string, r Runner) *Engine {
	e := &Engine{Root: root, Runner: r}
	e.Probe = func(ctx context.Context, d Domain) (ProbeResult, error) { return Probe(ctx, "127.0.0.1", d) }
	return e
}
func (e *Engine) path(p string) string {
	if e.Root == "/" {
		return p
	}
	return filepath.Join(e.Root, p)
}
func (e *Engine) configPath(n string) string {
	return e.path(vhostDir + "/block-beast-domain-" + n + ".conf")
}
func (e *Engine) Load() (State, error) {
	s := State{Version: 1, Domains: map[string]Domain{}}
	err := readJSON(e.path(statePath), &s)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if s.Version != 1 || s.Domains == nil {
		return s, fmt.Errorf("无效域名状态")
	}
	for n, d := range s.Domains {
		v, err := NormalizeDomain(n)
		if err != nil || v != n || d.Name != n {
			return s, fmt.Errorf("状态包含无效域名")
		}
		if _, err = RenderNginx(d); err != nil {
			return s, err
		}
	}
	return s, nil
}

type journal struct {
	Expected  map[string][]byte
	Touched   []string
	Live      bool
	Phase     string
	Snapshots []snapshot
	Restart   bool
	Old       State
	New       State
}

func (e *Engine) run(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return e.Runner.Run(ctx, name, args...)
}
func (e *Engine) reload(ctx context.Context) error {
	if err := e.run(ctx, nginxBinary, "-t", "-c", e.path(nginxMain)); err != nil {
		return err
	}
	return e.run(ctx, nginxBinary, "-s", "reload", "-c", e.path(nginxMain))
}
func (e *Engine) restart(ctx context.Context) error {
	return e.run(ctx, supervisorBinary, "restart", "block-beast-api", "block-beast-realtime")
}
func (e *Engine) rollback(ctx context.Context, j journal) error {
	var errs []error
	for _, s := range j.Snapshots {
		touched := false
		for _, p := range j.Touched {
			if p == s.Path {
				touched = true
			}
		}
		if !touched {
			continue
		}
		current, checkErr := capture(s.Path)
		if checkErr != nil {
			errs = append(errs, checkErr)
			continue
		}
		if current.Exists == s.Exists && bytes.Equal(current.Data, s.Data) {
			continue
		}
		expected, known := j.Expected[s.Path]
		if !known || current.Exists != (expected != nil) || !bytes.Equal(current.Data, expected) {
			errs = append(errs, fmt.Errorf("恢复冲突，文件已被外部修改: %s", s.Path))
			continue
		}
		if err := restore(s); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 && j.Live {
		if err := e.reload(ctx); err != nil {
			errs = append(errs, err)
		}
		if j.Restart {
			if err := e.restart(ctx); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) == 0 && j.Live {
		for _, d := range j.Old.Domains {
			if _, err := e.Probe(ctx, d); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("回滚失败，保留 pending.json: %w", errors.Join(errs...))
	}
	return os.Remove(e.path(journalPath))
}
func (e *Engine) lock() (*os.File, error) {
	p := e.path(lockPath)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("部署或域名操作正在进行")
	}
	return f, nil
}
func (e *Engine) prepareCertificate(r *Request) (map[string][]byte, error) {
	files := map[string][]byte{}
	if r.CertPath == "" && r.KeyPath == "" {
		return files, nil
	}
	if r.CertPath == "" || r.KeyPath == "" {
		return nil, fmt.Errorf("证书和私钥必须同时提供")
	}
	cert, err := os.ReadFile(e.path(r.CertPath))
	if err != nil {
		return nil, fmt.Errorf("无法读取证书")
	}
	key, err := os.ReadFile(e.path(r.KeyPath))
	if err != nil {
		return nil, fmt.Errorf("无法读取私钥")
	}
	if len(cert) > 1<<20 || len(key) > 1<<20 {
		return nil, fmt.Errorf("证书文件过大")
	}
	if err = ValidateCertificate(r.Domain, cert, key, e.Roots, time.Now()); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append(append([]byte(nil), cert...), key...))
	base := fmt.Sprintf("%s/certs/%x", stateDir, sum[:16])
	r.CertPath = base + "/fullchain.pem"
	r.KeyPath = base + "/privkey.pem"
	files[e.path(r.CertPath)] = cert
	files[e.path(r.KeyPath)] = key
	return files, nil
}
func (e *Engine) Apply(ctx context.Context, r Request) (err error) {
	r.Domain, err = NormalizeDomain(r.Domain)
	if err != nil {
		return err
	}
	if r.Operation == "replace" {
		r.OldDomain, err = NormalizeDomain(r.OldDomain)
		if err != nil {
			return err
		}
	}
	if !r.DryRun {
		lock, err := e.lock()
		if err != nil {
			return err
		}
		defer lock.Close()
		var pending journal
		if err = readJSON(e.path(journalPath), &pending); err == nil {
			if pending.Phase == "committed" {
				if err = os.Remove(e.path(journalPath)); err != nil {
					return err
				}
			} else if err = e.rollback(ctx, pending); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	} else if _, err := os.Stat(e.path(journalPath)); err == nil {
		return fmt.Errorf("存在未完成事务，请先执行恢复")
	}
	before, err := e.Load()
	if err != nil {
		return err
	}
	graph, err := e.configGraph()
	if err != nil {
		return err
	}
	if err = e.checkConflicts(graph, before, r); err != nil {
		return err
	}
	changes := map[string][]byte{}
	if r.Operation == "import" {
		if !safePath(r.Source) || filepath.Dir(r.Source) != vhostDir {
			return fmt.Errorf("导入源必须是宝塔站点目录中的配置文件")
		}
		source := e.path(r.Source)
		b, ok := graph[source]
		if !ok {
			return fmt.Errorf("源文件不是活动 Nginx 配置")
		}
		remaining, d, err := ImportDomain(b, r.Domain)
		if err != nil {
			return err
		}
		changes[source] = remaining
		if r.CertPath == "" {
			r.CertPath = d.CertPath
			r.KeyPath = d.KeyPath
		}
	}
	certFiles, err := e.prepareCertificate(&r)
	if err != nil {
		return err
	}
	for p, b := range certFiles {
		existing, readErr := os.ReadFile(p)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if readErr != nil || !bytes.Equal(existing, b) {
			changes[p] = b
		}
	}
	after, err := NextState(before, r)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(before, after) && len(changes) == 0 {
		return nil
	}
	for n, d := range after.Domains {
		b, err := RenderNginx(d)
		if err != nil {
			return err
		}
		changes[e.configPath(n)] = b
	}
	for n := range before.Domains {
		if _, ok := after.Domains[n]; !ok {
			changes[e.configPath(n)] = nil
		}
	}
	restart := !reflect.DeepEqual(ManagedOrigins(before), ManagedOrigins(after))
	if restart {
		if b, err := os.ReadFile(e.path(capabilityPath)); err != nil || string(b) != "1\n" {
			return fmt.Errorf("请先通过 deploy.sh 发布支持域名管理的版本")
		}
		b, _ := json.Marshal(struct {
			Version int      `json:"version"`
			Origins []string `json:"origins"`
		}{1, ManagedOrigins(after)})
		changes[e.path(originsPath)] = b
		env, err := os.ReadFile(e.path(envPath))
		if err != nil {
			return err
		}
		var lines []string
		for _, line := range strings.Split(string(env), "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "MANAGED_ORIGINS_FILE=") || strings.HasPrefix(trim, "export MANAGED_ORIGINS_FILE=") {
				continue
			}
			lines = append(lines, line)
		}
		lines = append(lines, "MANAGED_ORIGINS_FILE="+originsPath, "")
		changes[e.path(envPath)] = []byte(strings.Join(lines, "\n"))
	}
	b, _ := json.MarshalIndent(after, "", "  ")
	changes[e.path(statePath)] = b
	if r.DryRun {
		return nil
	}
	// Ensure this host's top-level include graph actually picks up new files.
	selected := false
	for _, b := range graph {
		ds, _ := parseNginx(b)
		_ = walkDirectives(ds, func(d directive) error {
			if d.words[0] == "include" && len(d.words) == 2 {
				pattern := d.words[1]
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(filepath.Dir(nginxMain), pattern)
				}
				if ok, _ := filepath.Match(pattern, vhostDir+"/block-beast-domain-"+r.Domain+".conf"); ok {
					selected = true
				}
			}
			return nil
		})
	}
	if !selected {
		return fmt.Errorf("主配置未包含受管站点目录")
	}
	keys := make([]string, 0, len(changes))
	for p := range changes {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	j := journal{Phase: "prepared", Restart: restart, Old: before, New: after, Expected: changes}
	for _, p := range keys {
		s, err := capture(p)
		if err != nil {
			return err
		}
		j.Snapshots = append(j.Snapshots, s)
	}
	if err = writeJSON(e.path(journalPath), j); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if recoverErr := e.rollback(recoveryCtx, j); recoverErr != nil {
				err = errors.Join(err, recoverErr)
			}
		}
	}()
	touch := func(p string) error {
		for _, old := range j.Touched {
			if old == p {
				return nil
			}
		}
		j.Touched = append(j.Touched, p)
		return writeJSON(e.path(journalPath), j)
	}
	// Immutable versioned certificate files must exist for nginx -t.
	for p, b := range certFiles {
		if err = touch(p); err != nil {
			return err
		}
		if err = atomicWrite(p, b, 0600); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp(e.path(stateDir), "check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	candidate, err := e.shadowConfig(dir, graph, changes)
	if err != nil {
		return err
	}
	if err = e.run(ctx, nginxBinary, "-t", "-c", candidate); err != nil {
		return err
	}
	// Refuse to overwrite a file changed outside the shared deployment lock.
	for _, old := range j.Snapshots {
		if _, certificate := certFiles[old.Path]; certificate {
			continue
		}
		current, checkErr := capture(old.Path)
		if checkErr != nil || !reflect.DeepEqual(old, current) {
			return fmt.Errorf("预检期间文件被修改，已停止: %s", old.Path)
		}
	}
	writeChange := func(p string, b []byte) error {
		if err := touch(p); err != nil {
			return err
		}
		if b == nil {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		mode := os.FileMode(0600)
		if strings.HasSuffix(p, ".conf") {
			mode = 0644
		}
		if p == e.path(envPath) {
			info, err := os.Stat(p)
			if err != nil {
				return err
			}
			mode = info.Mode().Perm()
		}
		if err := atomicWrite(p, b, mode); err != nil {
			return err
		}
		for _, old := range j.Snapshots {
			if old.Path == p && old.Exists {
				if err := os.Chown(p, old.UID, old.GID); err != nil {
					return err
				}
			}
		}
		if p == e.path(originsPath) && e.Root == "/" {
			group, err := user.LookupGroup("blockbeast")
			if err != nil {
				return err
			}
			gid, err := strconv.Atoi(group.Gid)
			if err != nil {
				return err
			}
			if err = os.Chown(p, 0, gid); err != nil {
				return err
			}
			return os.Chmod(p, 0640)
		}
		return nil
	}
	for _, p := range keys {
		if p == e.path(statePath) || r.Operation == "replace" && p == e.configPath(r.OldDomain) {
			continue
		}
		if err = writeChange(p, changes[p]); err != nil {
			return err
		}
	}
	j.Live = true
	j.Phase = "applied"
	if err = writeJSON(e.path(journalPath), j); err != nil {
		return err
	}
	if err = e.reload(ctx); err != nil {
		return err
	}
	if restart {
		if err = e.restart(ctx); err != nil {
			return err
		}
	}
	for _, d := range after.Domains {
		if _, err = e.Probe(ctx, d); err != nil {
			return err
		}
	}
	if r.Operation == "replace" {
		if err = writeChange(e.configPath(r.OldDomain), nil); err != nil {
			return err
		}
		if err = e.reload(ctx); err != nil {
			return err
		}
	}
	j.Phase = "verified"
	if err = writeJSON(e.path(journalPath), j); err != nil {
		return err
	}
	if err = writeChange(e.path(statePath), changes[e.path(statePath)]); err != nil {
		return err
	}
	j.Phase = "committed"
	if err = writeJSON(e.path(journalPath), j); err != nil {
		return err
	}
	// Keep a restricted snapshot of the previous state for manual recovery.
	if err = writeJSON(e.path(stateDir+"/last-operation.json"), j); err != nil {
		return err
	}
	return os.Remove(e.path(journalPath))
}

// Apply is the non-interactive remote execution entry point.
func Apply(ctx context.Context, root string, r Request, runner Runner) error {
	return NewEngine(root, runner).Apply(ctx, r)
}
