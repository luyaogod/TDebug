# fgl-fixtures — 4GL 大纲用例(抄自 BDL)

本目录是 **BDL 扩展**(`D:\我的项目\BDL`,同作者,MIT)的 `test/fixtures/` 原样副本:
61 组 `<name>.4gl` + `<name>.expected.json`,共 122 个文件(约 61 KB)。

- **用途**:`web/scripts/fgloutline.fixtures.mjs`(即 `npm run check:outline`)用它给
  `web/src/fgloutline.ts`(移植自 BDL 的大纲解析器)做文档级对拍。
- **期望值来源**:由 BDL 官方文档逐条推导(每个 `.expected.json` 的 `doc` / `about` 字段注明了出处),
  **不是**实现跑出来的快照。
- **基线**:与 BDL 自身 `scripts/fixture-check.cjs` 的实测结果一致 ——
  `用例 61 个:通过 58,已知偏差 3,不符 0`。
- **deviation 字段**:声明「文档上应当如此、实现已知未做到」的偏差(如 `SUBDIALOG` 不建节点、
  `;` 不作终止符、`record.*` 标签丢 `.*`)。默认不计失败,`--strict` 可把它们也算作失败。
- **改动约定**:
  - 改了 `fgloutline.ts` 后跑 `npm run check:outline`;**不符时先查实现**。
  - 确实要改期望,必须同时补 `deviation` 说明,不许把实现输出直接回写当期望。
  - 要与 BDL 保持 1:1 可比,所以**不要**在本目录增删用例;TDebug 自己的补充用例写在
    `fgltokens.test.mjs` 里(高亮对抗用例),或另建目录。

重新同步(若 BDL 侧更新了夹具):

```powershell
Copy-Item D:\我的项目\BDL\test\fixtures\*.4gl            web\scripts\fgl-fixtures\ -Force
Copy-Item D:\我的项目\BDL\test\fixtures\*.expected.json  web\scripts\fgl-fixtures\ -Force
```
