package debug

// 源码本地镜像的单测。
//
// 镜像是给 AI 的便利副本:调试过程中读过的文件(含程序停过的文件)在本地留一份,
// 好让它整读、本地搜,不必为每个文件再走一次 SSH。这里钉的是它不会出事的那几处 ——
// 路径拼装不能越界、环境名要能当目录名、清空要真的清干净。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 镜像路径由服务器路径拼成,`..` 必须被消掉 —— 否则能从镜像根逃出去写任意位置
func TestSrcMirrorPathNoEscape(t *testing.T) {
	got := srcMirrorPath(`D:\data`, "env", "/u1/../../../etc/passwd")
	if strings.Contains(got, "..") {
		t.Errorf("路径含 .. 可以越界: %s", got)
	}
	if !strings.HasPrefix(got, filepath.Join(`D:\data`, "srccache", "env")) {
		t.Errorf("没有落在镜像根下: %s", got)
	}
	// 保留原始目录层次,方便人肉对照服务器路径
	want := filepath.Join(`D:\data`, "srccache", "env", "u1", "t35prd", "com", "wss", "4gl", "wssp01131.4gl")
	if p := srcMirrorPath(`D:\data`, "env", "/u1/t35prd/com/wss/4gl/wssp01131.4gl"); p != want {
		t.Errorf("路径拼接错误\n 得到 %s\n 期望 %s", p, want)
	}
	// 没有数据目录(未配置)时不落镜像,而不是落到进程当前目录
	if p := srcMirrorPath("", "env", "/u1/a.4gl"); p != "" {
		t.Errorf("dataDir 为空应返回空串,得到 %s", p)
	}
	if p := srcMirrorPath(`D:\data`, "env", ""); p != "" {
		t.Errorf("服务器路径为空应返回空串,得到 %s", p)
	}
}

func TestPathSafeSeg(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"asf", "asf"},
		{"恒烁测试区", "恒烁测试区"},
		{"a/b", "a_b"},
		{"..", "_"},
		{".", "_"},
		{"", "_"},
		{"a:b*c?", "a_b_c_"},
	} {
		if got := pathSafeSeg(c.in); got != c.want {
			t.Errorf("pathSafeSeg(%q) = %q,期望 %q", c.in, got, c.want)
		}
	}
}

// 环境名可能是"恒烁测试区"这类中文,也可能为空(退化成 主机-区域)
func TestMirrorEnvSeg(t *testing.T) {
	if got := mirrorEnvSeg("恒烁测试区", "10.1.2.3", "36"); got != "恒烁测试区" {
		t.Errorf("中文环境名应保留,得到 %q", got)
	}
	if got := mirrorEnvSeg("", "10.1.2.3", "36"); got != "10.1.2.3-36" {
		t.Errorf("空环境名应退化为 主机-区域,得到 %q", got)
	}
	// 环境名里的路径分隔符不能变成目录层级
	if got := mirrorEnvSeg("a/b:c", "h", "36"); strings.ContainsAny(got, `/\:`) {
		t.Errorf("环境名里的分隔符应被替换,得到 %q", got)
	}
	if got := mirrorEnvSeg("..", "h", "36"); got != "_" {
		t.Errorf("环境名 .. 应被消掉,得到 %q", got)
	}
}

func TestWriteAndClearMirror(t *testing.T) {
	dir := t.TempDir()
	src := []byte("MAIN\n  DISPLAY \"x\"\nEND MAIN\n")
	p := writeMirror(dir, "env", "/u1/erp/asf/4gl/asf_x.4gl", src)
	if p == "" {
		t.Fatal("应写出镜像并返回路径")
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if string(got) != string(src) {
		t.Errorf("内容不一致:\n得到 %q\n期望 %q", got, src)
	}
	// 不留 .tmp 残骸(原子落位)
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("落位后不应留下 .tmp")
	}

	clearMirror(dir)
	if _, err := os.Stat(filepath.Join(dir, srcMirrorDir)); !os.IsNotExist(err) {
		t.Error("clearMirror 应清掉整个镜像目录")
	}
	// 没建过镜像时清理不该报错(每轮调试开始都会调一次)
	clearMirror(dir)
	clearMirror("")
}

// 同一服务器路径重复写要覆盖,不能留 .tmp 堆积
func TestWriteMirrorOverwrites(t *testing.T) {
	dir := t.TempDir()
	writeMirror(dir, "env", "/u1/a.4gl", []byte("第一版"))
	p := writeMirror(dir, "env", "/u1/a.4gl", []byte("第二版"))
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if string(got) != "第二版" {
		t.Errorf("重复写应覆盖,得到 %q", got)
	}
}

// mirrorStopFile 的两道闸:没有数据目录、或停站没有文件名,都不该动手。
// 前者防的是"凭空在进程当前目录建 srccache",后者是入口停站的真实情况
// (fgldb 在程序跑起来之前给不出位置)。
func TestMirrorStopFileGuards(t *testing.T) {
	s := waitTestSession() // cfgFor 不带 DataDir
	s.mirrorStopFile(&StopInfo{File: "asf_x.4gl"})
	if s.mirrored != nil {
		t.Error("没有数据目录时不该记录已镜像,更不该开工")
	}
	s.mirrorStopFile(nil)
	s.mirrorStopFile(&StopInfo{})

	// 有数据目录但没有文件名:同样不动作
	s2 := waitTestSession()
	s2.cfg.DataDir = t.TempDir()
	s2.mirrorStopFile(&StopInfo{Reason: "entry"})
	if s2.mirrored != nil {
		t.Error("停站没有文件名时不该记录已镜像")
	}
	if _, err := os.Stat(filepath.Join(s2.cfg.DataDir, srcMirrorDir)); !os.IsNotExist(err) {
		t.Error("不该建出镜像目录")
	}
}

// 清目录与清记录必须成对:复用的宿主会话会把"已镜像"记录带进下一轮,
// 不同步清掉,新一轮就再也落不下任何副本。
func TestResetMirrored(t *testing.T) {
	s := waitTestSession()
	s.mirrored = map[string]bool{"asf_x.4gl": true}
	s.resetMirrored()
	if len(s.mirrored) != 0 {
		t.Errorf("应清空已镜像记录,实际 %v", s.mirrored)
	}
}
