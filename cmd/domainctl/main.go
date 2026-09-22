// domainctl is installed by deploy.sh and invoked over the existing SSH channel.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/platform/domainops"
)

type originsFlag []string

func (o *originsFlag) String() string { return strings.Join(*o, ",") }
func (o *originsFlag) Set(v string) error {
	n, e := domainops.NormalizeOrigin(v)
	if e == nil {
		*o = append(*o, n)
	}
	return e
}

var remoteDirRE = regexp.MustCompile(`^/tmp/block-beast-domain-[a-f0-9]{16,64}$`)

func prepare(output, remote string, args []string) (domainops.Request, error) {
	var r domainops.Request
	if !remoteDirRE.MatchString(remote) {
		return r, fmt.Errorf("上传目录无效")
	}
	if len(args) == 0 {
		return r, fmt.Errorf("缺少操作")
	}
	r.Operation = args[0]
	args = args[1:]
	switch r.Operation {
	case "list":
		if len(args) != 0 {
			return r, fmt.Errorf("list 不接受参数")
		}
	case "add", "remove", "certificate", "import", "replace":
		if r.Operation == "replace" {
			if len(args) < 2 {
				return r, fmt.Errorf("replace 需要旧域名和新域名")
			}
			var err error
			r.OldDomain, err = domainops.NormalizeDomain(args[0])
			if err != nil {
				return r, err
			}
			args = args[1:]
		}
		if len(args) == 0 {
			return r, fmt.Errorf("缺少域名")
		}
		var err error
		r.Domain, err = domainops.NormalizeDomain(args[0])
		if err != nil {
			return r, err
		}
		args = args[1:]
	default:
		return r, fmt.Errorf("支持 add/replace/remove/certificate/import/list")
	}
	fs := flag.NewFlagSet("domainctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cert, key, remoteCert, remoteKey, session string
	var origins originsFlag
	fs.StringVar(&cert, "cert", "", "")
	fs.StringVar(&key, "key", "", "")
	fs.StringVar(&remoteCert, "remote-cert", "", "")
	fs.StringVar(&remoteKey, "remote-key", "", "")
	fs.StringVar(&r.Source, "source", "", "")
	fs.StringVar(&session, "session-file", "", "")
	fs.BoolVar(&r.DryRun, "dry-run", false, "")
	fs.Var(&origins, "allow-origin", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return r, fmt.Errorf("参数无效；证书使用 --cert/--key，来源使用 --allow-origin")
	}
	if (cert == "") != (key == "") || (remoteCert == "") != (remoteKey == "") || cert != "" && remoteCert != "" {
		return r, fmt.Errorf("本地或远端证书与私钥须成对，不能混用")
	}
	if r.Operation == "remove" && (cert != "" || remoteCert != "" || len(origins) > 0 || r.Source != "" || session != "") {
		return r, fmt.Errorf("remove 不接受证书或来源参数")
	}
	if r.Operation == "certificate" && cert == "" && remoteCert == "" {
		return r, fmt.Errorf("certificate 需要证书")
	}
	if (r.Operation == "import") != (r.Source != "") {
		return r, fmt.Errorf("import 必须且只能通过 --source 指定既有站点")
	}
	r.Origins = origins
	r.CertPath = remoteCert
	r.KeyPath = remoteKey
	if cert != "" {
		c, err := os.ReadFile(cert)
		if err != nil {
			return r, fmt.Errorf("读取本地证书失败")
		}
		k, err := os.ReadFile(key)
		if err != nil {
			return r, fmt.Errorf("读取本地私钥失败")
		}
		if len(c) > 1<<20 || len(k) > 1<<20 {
			return r, fmt.Errorf("证书文件过大")
		}
		if err = domainops.ValidateCertificate(r.Domain, c, k, nil, time.Now()); err != nil {
			return r, err
		}
		if r.DryRun {
			r.LocalTLSChecked = true
		} else {
			if err = os.WriteFile(filepath.Join(output, "fullchain.pem"), c, 0600); err != nil {
				return r, err
			}
			if err = os.WriteFile(filepath.Join(output, "privkey.pem"), k, 0600); err != nil {
				return r, err
			}
			r.CertPath = remote + "/fullchain.pem"
			r.KeyPath = remote + "/privkey.pem"
		}
	}
	if session != "" && !r.DryRun {
		info, err := os.Stat(session)
		if err != nil || info.Mode().Perm() != 0600 {
			return r, fmt.Errorf("测试会话文件必须为 0600")
		}
		b, err := os.ReadFile(session)
		if err != nil || len(b) > 16384 || strings.TrimSpace(string(b)) == "" {
			return r, fmt.Errorf("测试会话无效")
		}
		if err = os.WriteFile(filepath.Join(output, "session.txt"), b, 0600); err != nil {
			return r, err
		}
		r.SessionPath = remote + "/session.txt"
	}
	b, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	return r, os.WriteFile(filepath.Join(output, "request.json"), b, 0600)
}
func readRequest(src io.Reader) (domainops.Request, error) {
	var r domainops.Request
	dec := json.NewDecoder(io.LimitReader(src, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("请求 JSON 无效")
	}
	var v any
	if dec.Decode(&v) != io.EOF {
		return r, fmt.Errorf("请求包含额外数据")
	}
	return r, nil
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("需要 prepare/apply/check/capabilities/validate-origins")
	}
	if args[0] == "capabilities" {
		fmt.Println("1")
		return nil
	}
	if args[0] == "prepare" {
		if len(args) < 4 {
			return errors.New("prepare 需要输出目录、上传目录及操作")
		}
		r, err := prepare(args[1], args[2], args[3:])
		if err != nil {
			return err
		}
		if r.DryRun {
			fmt.Println("dry-run")
		} else {
			fmt.Println(r.Operation)
		}
		return nil
	}
	if args[0] == "validate-origins" {
		if len(args) != 2 {
			return errors.New("缺少来源文件")
		}
		cfg := config.Config{ManagedOriginsFile: args[1]}
		return cfg.LoadManagedOrigins()
	}
	if args[0] != "apply" && args[0] != "check" {
		return errors.New("未知执行模式")
	}
	if os.Geteuid() != 0 {
		return errors.New("远端配置操作需要 root")
	}
	var src io.Reader = os.Stdin
	if len(args) > 2 {
		return errors.New("参数过多")
	}
	if len(args) == 2 {
		f, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	r, err := readRequest(src)
	if err != nil {
		return err
	}
	if args[0] == "check" && !r.DryRun && r.Operation != "list" {
		return errors.New("check 只接受 dry-run 或 list")
	}
	e := domainops.NewEngine("/", domainops.ExecRunner{})
	if r.Operation == "list" {
		return e.List(os.Stdout)
	}
	if r.LocalTLSChecked && !r.DryRun {
		return errors.New("本地证书预检标记仅限 dry-run")
	}
	if r.DryRun && r.LocalTLSChecked && r.Operation == "certificate" {
		state, err := e.Load()
		if err != nil {
			return err
		}
		if _, exists := state.Domains[r.Domain]; !exists {
			return errors.New("更新证书需要受管域名")
		}
		// Check existing ingress/conflicts without transmitting private material.
		r.Operation = "add"
	}

	if r.SessionPath != "" {
		info, err := os.Stat(r.SessionPath)
		if err != nil || info.Mode().Perm() != 0600 {
			return errors.New("测试会话文件权限必须为0600")
		}
		b, err := os.ReadFile(r.SessionPath)
		if err != nil || len(b) > 16384 {
			return errors.New("读取测试会话失败")
		}
		token := strings.TrimSpace(string(b))
		e.Probe = func(ctx context.Context, d domainops.Domain) (domainops.ProbeResult, error) {
			return domainops.ProbeWithSession(ctx, "127.0.0.1", d, token)
		}
	}
	if err = e.Apply(context.Background(), r); err != nil {
		return err
	}
	if r.DryRun {
		fmt.Println("只读预检通过；未改文件、未重载；Nginx 候选检查和 HTTP/TLS/WS 在线验收将在应用时执行")
		if r.LocalTLSChecked {
			fmt.Println("本地证书校验通过，私钥未上传；远端 TLS 配置未验收")
		}
		return nil
	}
	fmt.Println("操作完成；服务器路由检查通过，公网 DNS/访问未验证；未提供测试会话时 WS 仅验证未认证路由")
	return e.List(os.Stdout)
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
