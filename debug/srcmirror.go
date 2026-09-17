package debug

// 源码本地镜像 + 会话外读取连接复用。
//
// 镜像:<DataDir>/srccache/<环境>/<服务器路径>。**这轮调试经历过的文件**都留一份本地拷贝,
// 并在返回值里带出本地路径,好让 AI 整读、反复搜,不必为每个文件再走一次 SSH、
// 也不必迁就 source 的行段读取。
//
// 两个来源:程序停过的文件(mirrorStopFile,每轮调试的真实经历)与显式读过的文件
// (ResolveSource / ReadSourceStandalone)。
//
// 它**只活一轮调试**:每轮启动前清空整个镜像。服务器上的源码是会改的,
// 留着上一轮的文件只会让 AI 拿着过期代码推理 —— 宁可贵一点重拉。
// 权威性始终在 tdebug source(每次真读服务器)那一侧。
// 环境进路径是为了隔离测试区/正式区:同名文件在两个区的登录路径与内容都可能不同。
//
// 整个代码库的检索不在本工具的范围内。

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"tdebug/host"

	"github.com/pkg/sftp"
)

// srcMirrorDir 镜像根目录名(位于 DataDir 下,与断点存档同级)
const srcMirrorDir = "srccache"

// pathSafeSeg 把任意字符串压成单个安全的路径段。
// 保留中文与常见符号(环境名可能是"恒烁测试区"),只替换分隔/通配类字符。
func pathSafeSeg(s string) string {
	out := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '_'
		}
		return r
	}, s)
	out = strings.TrimSpace(out)
	// 兜底:空、当前目录、上级目录都不能作为路径段(防 `..` 逃出镜像根)
	if out == "" || out == "." || out == ".." {
		return "_"
	}
	return out
}

// mirrorEnvSeg 镜像目录里的环境段:优先环境名,退化为 主机-区域。
func mirrorEnvSeg(envName, sshHost, zone string) string {
	s := strings.TrimSpace(envName)
	if s == "" {
		s = sshHost
		if zone != "" {
			s += "-" + zone
		}
	}
	return pathSafeSeg(s)
}

// srcMirrorPath 服务器绝对路径 → 本地镜像绝对路径。
// 后缀沿用服务器路径(去掉前导 /)以保留目录层次,方便人肉对照;逐段清洗防越界。
func srcMirrorPath(dataDir, envSeg, serverPath string) string {
	if dataDir == "" || serverPath == "" {
		return ""
	}
	rel := strings.TrimPrefix(filepath.ToSlash(serverPath), "/")
	if rel == "" {
		return ""
	}
	segs := strings.Split(rel, "/")
	for i, sg := range segs {
		segs[i] = pathSafeSeg(sg)
	}
	return filepath.Join(append([]string{dataDir, srcMirrorDir, envSeg}, segs...)...)
}

// writeMirror 原子落盘镜像并返回本地路径;写不进去返回空串。
// 镜像只是给 AI 的便利副本,任何失败都只记录不报错 —— 绝不能因为它读不到源码。
func writeMirror(dataDir, envSeg, serverPath string, data []byte) string {
	p := srcMirrorPath(dataDir, envSeg, serverPath)
	if p == "" {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		log.Printf("[srccache] 建目录失败 %s: %v", filepath.Dir(p), err)
		return ""
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[srccache] 写镜像失败 %s: %v", tmp, err)
		return ""
	}
	if err := os.Rename(tmp, p); err != nil {
		log.Printf("[srccache] 落位失败 %s: %v", p, err)
		return ""
	}
	return p
}

// srcMirrorRoot 镜像根目录(<DataDir>/srccache)
func srcMirrorRoot(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, srcMirrorDir)
}

// execLogDir 命令完整输出的落盘目录(<DataDir>/execlog)。
// 与源码镜像分开放:一个是"读过的源码",一个是"跑过的命令输出",生命周期相同但语义不同。
const execLogDir = "execlog"

// writeExecLog 把一次命令的完整输出落成本地文件,返回路径;写不进去返回空串。
//
// 为什么要有它:回给调用方的正文有上限(超了就只给头部),但"完整内容"必须留得住 ——
// 否则想多看一点就得重跑命令,而重跑会改变现场。落本地之后可以随时 grep/整读,零往返。
// 与镜像同款:原子落位、失败只记录不报错。
func writeExecLog(dataDir, envSeg, name string, data []byte) string {
	if dataDir == "" || name == "" {
		return ""
	}
	dir := filepath.Join(dataDir, execLogDir, pathSafeSeg(envSeg))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[execlog] 建目录失败 %s: %v", dir, err)
		return ""
	}
	p := filepath.Join(dir, pathSafeSeg(name))
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[execlog] 写失败 %s: %v", tmp, err)
		return ""
	}
	if err := os.Rename(tmp, p); err != nil {
		log.Printf("[execlog] 落位失败 %s: %v", p, err)
		return ""
	}
	return p
}

// clearMirror 清空本轮调试攒下的所有本地副本:
//   - srccache/:读过的源码(代码会更新,跨轮次留着只会误导判断);
//   - execlog/:跑过的命令输出(同理,而且它随轮次增长没有意义)。
//
// 每轮启动调试前调用,失败只记录。
func clearMirror(dataDir string) {
	if dataDir == "" {
		return
	}
	for _, root := range []string{srcMirrorRoot(dataDir), filepath.Join(dataDir, execLogDir)} {
		if err := os.RemoveAll(root); err != nil {
			log.Printf("[srccache] 清理失败 %s: %v", root, err)
		}
	}
}

// ---------- 会话外读取连接复用 ----------

// srcConn 会话外读取共用的连接。
// 以前每次 tdebug source 都 host.Dial 一次:连着读十几段源码就是十几次完整 SSH 握手。
type srcConn struct {
	conn *host.SSHConn
	cl   *sftp.Client
	key  string
}

func (c *srcConn) close() {
	if c == nil {
		return
	}
	if c.cl != nil {
		_ = c.cl.Close()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// srcConnKey 连接归属:目标或账号变了就换连接。
func (m *Manager) srcConnKey() string {
	return m.cfg.SSH.Addr() + "/" + m.cfg.SSH.User
}

// withSrcConn 借出会话外读取连接执行 fn。
//
// 借用前先探活(SFTP 一次 RealPath 往返):空闲连接会被服务端回收,网络也可能断,
// 探活失败就丢弃重建。用探活而不是"失败后重试",是为了让调用方不必关心连接生命周期
// —— 探活那一次往返远比重新握手便宜。
func (m *Manager) withSrcConn(fn func(*host.SSHConn, *sftp.Client) error) error {
	conn, cl, err := m.srcConn()
	if err != nil {
		return err
	}
	return fn(conn, cl)
}

// srcConn 取(或建立)会话外读取连接
func (m *Manager) srcConn() (*host.SSHConn, *sftp.Client, error) {
	key := m.srcConnKey()
	m.srcMu.Lock()
	cur := m.src
	m.srcMu.Unlock()

	if cur != nil && cur.key == key {
		if _, err := cur.cl.RealPath("."); err == nil {
			// 连接健在:动态路径仍要按当前配置确保(会话切了区域时 Runtime 可能被重置)
			if err := m.ensureRuntimeEnv(cur.conn); err != nil {
				return nil, nil, err
			}
			return cur.conn, cur.cl, nil
		}
		m.dropSrcConn(cur) // 死连接:关掉重建
	}

	conn, err := host.Dial(m.cfg.SSH)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	// 动态路径(登录区域 → 环境脚本):探针+缓存;失败即报错(无静态配置可回退)
	if err := m.ensureRuntimeEnv(conn); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	cl, err := conn.SFTP()
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("打开 SFTP 失败: %w", err)
	}
	nc := &srcConn{conn: conn, cl: cl, key: key}
	m.srcMu.Lock()
	old := m.src
	m.src = nc
	m.srcMu.Unlock()
	old.close() // 换目标后旧连接没人用了
	return conn, cl, nil
}

// dropSrcConn 丢弃缓存连接(仅当它仍是当前缓存的那条,免得误关刚建好的)
func (m *Manager) dropSrcConn(c *srcConn) {
	if c == nil {
		return
	}
	m.srcMu.Lock()
	if m.src == c {
		m.src = nil
	}
	m.srcMu.Unlock()
	c.close()
}
