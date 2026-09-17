#!/usr/bin/env python3
# 把便携版暂存目录打成 zip。
#
# 为什么单独成一个脚本:原先是在 build_portable.bat 里塞一句 python -c,用的是
# os.listdir + z.write —— 那个组合**不会递归**,目录只会写进一个空条目:
# skills/ 下明明有 SKILL.md,打出来的包里却只有一个空的 skills/ 目录,
# 便携包于是缺了技能文档(而它正是给 AI 用的说明书)。
#
# 路径分隔符固定用 /(zip 规范),不依赖平台。

import os
import sys
import zipfile

STAGE = sys.argv[1] if len(sys.argv) > 1 else "dist/tdebug-portable"
OUT = sys.argv[2] if len(sys.argv) > 2 else "dist/tdebug-portable.zip"
ROOT = os.path.basename(os.path.normpath(STAGE))  # zip 里的顶层目录名

if not os.path.isdir(STAGE):
    sys.exit("暂存目录不存在: " + STAGE)

n = 0
with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED) as z:
    for dirpath, _dirnames, filenames in os.walk(STAGE):
        for fn in filenames:
            p = os.path.join(dirpath, fn)
            rel = os.path.relpath(p, STAGE).replace(os.sep, "/")
            z.write(p, ROOT + "/" + rel)
            n += 1
print("已打包 %d 个文件 -> %s" % (n, OUT))
