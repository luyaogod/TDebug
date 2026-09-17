package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// mkSkills 在 dir 下建一个技能源树:每个技能一个目录 + SKILL.md。
func mkSkills(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: "+n+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListSkills(t *testing.T) {
	src := t.TempDir()
	mkSkills(t, src, "beta", "alpha")
	got, err := listSkills(src)
	if err != nil {
		t.Fatalf("listSkills: %v", err)
	}
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("listSkills = %v, want [alpha beta](按名字排序)", got)
	}

	// 技能目录缺 SKILL.md 必须报错
	if err := os.MkdirAll(filepath.Join(src, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := listSkills(src); err == nil {
		t.Fatal("技能目录缺 SKILL.md 时应报错")
	}

	// 空源目录必须报错
	if _, err := listSkills(t.TempDir()); err == nil {
		t.Fatal("skills 源目录为空时应报错")
	}
}

func TestInstallSkillsTreeCopies(t *testing.T) {
	src := t.TempDir()
	mkSkills(t, src, "alpha", "beta")
	dst := filepath.Join(t.TempDir(), "skills")

	copied, err := installSkillsTree(src, dst, false)
	if err != nil {
		t.Fatalf("installSkillsTree: %v", err)
	}
	if len(copied) != 2 {
		t.Fatalf("copied = %v, want 2 项", copied)
	}
	// 装完必须是「技能名/SKILL.md」形态,不是扁平 .md
	for _, n := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(dst, n, "SKILL.md")); err != nil {
			t.Fatalf("%s/SKILL.md 不存在: %v", n, err)
		}
	}
}

func TestInstallSkillsTreeConflict(t *testing.T) {
	src := t.TempDir()
	mkSkills(t, src, "alpha")
	dst := t.TempDir()
	mkSkills(t, dst, "alpha") // 目标已有同名技能

	if _, err := installSkillsTree(src, dst, false); err == nil {
		t.Fatal("目标已存在同名技能目录时,不加 --force 应拒绝")
	}
	if _, err := installSkillsTree(src, dst, true); err != nil {
		t.Fatalf("--force 应覆盖: %v", err)
	}
}

func TestInstallSkillsTreeRejectsSameDir(t *testing.T) {
	src := t.TempDir()
	mkSkills(t, src, "alpha")
	if _, err := installSkillsTree(src, src, true); err == nil {
		t.Fatal("目标与源相同应报错(而不是自我覆盖)")
	}
}

func TestMergeUserPath(t *testing.T) {
	cases := []struct {
		old, dir  string
		want      string
		wantAdded bool
	}{
		{"", `D:\a`, `D:\a`, true},
		{`C:\x`, `D:\a`, `C:\x;D:\a`, true},
		{`C:\x;`, `D:\a`, `C:\x;D:\a`, true},                      // 去尾分号再追加
		{`C:\x;D:\a`, `D:\a`, `C:\x;D:\a`, false},                 // 已存在
		{`C:\x;D:\A\`, `d:\a`, `C:\x;D:\A\`, false},               // 大小写 + 尾分隔符视为同一项
		{`%USERPROFILE%\b`, `D:\a`, `%USERPROFILE%\b;D:\a`, true}, // 展开式变量原样保留
	}
	for _, c := range cases {
		got, added := mergeUserPath(c.old, c.dir)
		if got != c.want || added != c.wantAdded {
			t.Errorf("mergeUserPath(%q, %q) = (%q, %v), want (%q, %v)", c.old, c.dir, got, added, c.want, c.wantAdded)
		}
	}
}
