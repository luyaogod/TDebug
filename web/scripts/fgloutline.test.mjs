// fgloutline 解析器单测:验证函数/交互块/子块解析与忽略规则。
// 运行:cd web && npx esbuild scripts/fgloutline.test.mjs --bundle --format=esm \
//   --platform=node --loader:.mjs=ts --outfile=scripts/.t.mjs && node scripts/.t.mjs
import { parseOutline, type OutlineNode } from '../src/fgloutline'

const SRC = `DATABASE form9
-- 模块注释
MAIN
  DEFINE a INT
  DIALOG ATTRIBUTES(UNBUFFERED)
    INPUT BY NAME cust.*  # 行注释
    END INPUT
    BEFORE DIALOG
      MESSAGE "start"
    ON ACTION close
      EXIT DIALOG
  END DIALOG
END MAIN

FUNCTION bsft001_wf()
  CONSTRUCT BY NAME q.* WHERE 1=1
  INPUT BY NAME a.*
    BEFORE FIELD amt
      MESSAGE "x"
    ON ACTION zoom
      CALL zoom()
    AFTER INPUT
      CALL check()
  END INPUT
  MENU "m"
    COMMAND "q"
      CALL q()
  END MENU
  { 块注释
    INPUT BY NAME fake.*
  }
  INPUT BY NAME single
END FUNCTION

REPORT rpt1()
  FORMAT
    ON EVERY ROW
      PRINT x
END REPORT

PRIVATE FUNCTION helper(p INT) RETURNS INT
  DISPLAY ARRAY arr TO sa.*
    ON ACTION accept
      EXIT DISPLAY
  END DISPLAY
  RETURN p
END FUNCTION

PRIVATE DIALOG dlg1(p INT)
  INPUT ARRAY sa FROM s.*
    BEFORE ROW
      MESSAGE "r"
  END INPUT
END DIALOG
`

const tree = parseOutline(SRC)
const dump = (ns: OutlineNode[], d = 0): string[] =>
  ns.flatMap((n) => [`'${'  '.repeat(d)}${n.label} @${n.line}'`, ...dump(n.children || [], d + 1)])

const lines = dump(tree)
console.log(lines.join('\n'))

// 断言
let fail = 0
const expect = (cond: boolean, msg: string) => { if (!cond) { console.error('FAIL: ' + msg); fail++ } }

expect(tree.length === 4, `根节点 4 个(MAIN/函数/PRIVATE 函数/声明式 DIALOG),实际 ${tree.length}`)
expect(tree[0].label === 'MAIN' && tree[0].line === 3, 'MAIN @3')
const dlg = tree[0].children![0]
expect(dlg.label === 'DIALOG' && dlg.line === 5, 'MAIN 内过程式 DIALOG @5')
expect(dlg.children!.map((c) => c.label).join('|') === 'INPUT|BEFORE DIALOG|ON ACTION close', 'DIALOG 子节点顺序')
expect(dlg.children![0].line === 6, '内嵌 INPUT @6')
const fn = tree[1]
expect(fn.label === 'bsft001_wf()' && fn.line === 15, '函数 @15')
expect(fn.children![0].label === 'CONSTRUCT' && fn.children![0].children === undefined, '单语句 CONSTRUCT 为叶子')
const inp = fn.children![1]
expect(inp.label === 'INPUT' && inp.children!.length === 3, 'INPUT 3 个子块')
expect(inp.children!.map((c) => c.label).join('|') === 'BEFORE FIELD amt|ON ACTION zoom|AFTER INPUT', 'INPUT 子块')
expect(!fn.children!.some((c) => c.label === 'MENU'), 'MENU 不上树')
expect(!fn.children!.some((c) => c.label.includes('fake')), '块注释内容被剥离')
expect(fn.children!.some((c) => c.label === 'INPUT' && c.children === undefined && c.line === 32), '单语句 INPUT 叶子 @32')
expect(!tree.some((n) => n.label.includes('rpt1')), 'REPORT 不上树')
const helper = tree[2]
expect(helper.label === 'helper()' && helper.line === 41, 'PRIVATE FUNCTION @41')
expect(helper.children === undefined || !helper.children!.some((c) => c.label.includes('DISPLAY')), '独立 DISPLAY ARRAY 不上树')
const decl = tree[3]
expect(decl.label === 'DIALOG dlg1()' && decl.line === 49, '声明式 PRIVATE DIALOG @49')
expect(decl.children![0].label === 'INPUT ARRAY' && decl.children![0].children![0].label === 'BEFORE ROW', '声明式 DIALOG 内 INPUT ARRAY')

console.log(fail === 0 ? '\nALL PASS' : `\n${fail} FAILURES`)
process.exit(fail === 0 ? 0 : 1)
