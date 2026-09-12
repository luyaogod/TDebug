package host

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SSHConn 到 T100 服务器的 SSH 连接(一条连接可开多个通道:PTY/SFTP)
type SSHConn struct {
	cli *ssh.Client
	cfg SSHConfig
}

// Dial 建立 SSH 连接(密码认证)
func Dial(cfg SSHConfig) (*SSHConn, error) {
	cliCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 内网工具,与手工登录信任级别一致
		Timeout:         20 * time.Second,
	}
	cli, err := ssh.Dial("tcp", cfg.Addr(), cliCfg)
	if err != nil {
		return nil, err
	}
	return &SSHConn{cli: cli, cfg: cfg}, nil
}

// StartKeepalive 每 15 秒发一次心跳,连接意外断开时回调 onDead。
// 返回 stop 函数:主动关闭连接前先调用,避免「use of closed network connection」误报。
func (c *SSHConn) StartKeepalive(onDead func(error)) (stop func()) {
	stopCh := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				_, _, err := c.cli.SendRequest("keepalive@openssh.com", true, nil)
				if err != nil {
					select {
					case <-stopCh: // 主动关闭导致的失败:不刷日志不回调
					default:
						log.Printf("[ssh] keepalive 失败: %v", err)
						if onDead != nil {
							onDead(err)
						}
					}
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stopCh) }) }
}

// NewPTY 打开交互式终端会话(T100 登录 → 区域菜单 → shell → fglrun -d 都在这里面)
func (c *SSHConn) NewPTY(width, height int) (*PTYSession, error) {
	sess, err := c.cli.NewSession()
	if err != nil {
		return nil, err
	}
	// 保持与手工验证一致的默认终端模式(ECHO 开启,解析器已适配回显)
	if err := sess.RequestPty("vt100", width, height, ssh.TerminalModes{}); err != nil {
		sess.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	// PTY 模式下 stderr 自动汇入同一终端,无需单独接管
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, err
	}
	return &PTYSession{sess: sess, stdin: stdin, stdout: stdout}, nil
}

// SFTP 打开文件传输通道(拉取 .4gl 源码)
func (c *SSHConn) SFTP() (*sftp.Client, error) {
	return sftp.NewClient(c.cli)
}

// Output 执行一次性命令并返回合并输出(模块解析等轻量查询用;
// exec 通道不经过登录 profile,不能用于依赖 T100 环境变量的操作)
func (c *SSHConn) Output(cmd string, timeout time.Duration) (string, error) {
	sess, err := c.cli.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	done := make(chan error, 1)
	var out []byte
	go func() {
		out, err = sess.CombinedOutput(cmd)
		done <- err
	}()
	select {
	case e := <-done:
		if e != nil {
			return string(out), e
		}
		return string(out), nil
	case <-time.After(timeout):
		return "", fmt.Errorf("命令超时: %s", cmd)
	}
}

// Close 关闭连接
func (c *SSHConn) Close() error { return c.cli.Close() }

// Cfg 返回本连接的目标配置(供外部判断连接归属)
func (c *SSHConn) Cfg() SSHConfig { return c.cfg }

// PTYSession 交互式终端会话
type PTYSession struct {
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
}

// Write 写入终端(命令须自带 \r 结尾)
func (p *PTYSession) Write(s string) error {
	_, err := io.WriteString(p.stdin, s)
	return err
}

// Read 供输出泵读取;返回 n=0 且 err==nil 不应出现
func (p *PTYSession) Read(b []byte) (int, error) { return p.stdout.Read(b) }

// Close 关闭会话(远端子进程随之被回收)
func (p *PTYSession) Close() {
	p.stdin.Close()
	p.sess.Close()
}
