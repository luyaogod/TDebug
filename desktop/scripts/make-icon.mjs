// 生成桌面版图标(零依赖):程序化画 256×256 的 PNG,再包成 ICO。
//
// 图面呼应界面里的调试语义:深色圆角底 + 左侧槽位(红色断点圆点、琥珀色停站箭头)
// + 右侧几行代码(用编辑器主题里真实的语法色)。3 倍超采样后降采样得到抗锯齿。
// 想要自己的图标:把 256×256 的 icon.png 放到 desktop/build/ 覆盖即可(electron-builder
// 用 build/icon.ico,重跑本脚本会用新的 PNG 重新打包 ICO —— 注意本脚本会覆盖生成的 PNG)。
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { deflateSync, crc32 } from 'node:zlib'

const here = path.dirname(fileURLToPath(import.meta.url))
const outDir = path.resolve(here, '..', 'build')
const S = 3 // 超采样倍数
const W = 256
const N = W * S

// 主题色(与 web/src/index.css 的暗色主题一致)
const C = {
  bg: [31, 31, 31], // #1f1f1f = editor.background / --card
  edge: [59, 59, 59],
  plain: [212, 212, 212],
  keyword: [86, 156, 214], // #569cd6
  comment: [106, 153, 85], // #6a9955
  string: [206, 145, 120], // #ce9178
  bp: [239, 68, 68], // #ef4444 断点红点
  stop: [234, 179, 8], // #eab308 停站箭头
}

const buf = new Float32Array(N * N * 4) // RGBA,0..255

function blend(i, [r, g, b], a = 1) {
  const o = i * 4
  const sa = buf[o + 3] / 255
  // source-over(直通 alpha):out = (src*a + dst*sa*(1-a)) / na
  const na = a + sa * (1 - a)
  if (na <= 0) return
  buf[o] = (r * a + buf[o] * sa * (1 - a)) / na
  buf[o + 1] = (g * a + buf[o + 1] * sa * (1 - a)) / na
  buf[o + 2] = (b * a + buf[o + 2] * sa * (1 - a)) / na
  buf[o + 3] = na * 255
}

// 圆角矩形(坐标/半径按超采样后的像素)
function roundRect(x0, y0, x1, y1, r, color) {
  for (let y = Math.floor(y0); y < Math.ceil(y1); y++) {
    for (let x = Math.floor(x0); x < Math.ceil(x1); x++) {
      const cx = Math.min(Math.max(x + 0.5, x0 + r), x1 - r)
      const cy = Math.min(Math.max(y + 0.5, y0 + r), y1 - r)
      const d = Math.hypot(x + 0.5 - cx, y + 0.5 - cy)
      if (d > r) continue
      blend(y * N + x, color)
    }
  }
}
function circle(cx, cy, r, color) {
  for (let y = Math.floor(cy - r); y < Math.ceil(cy + r); y++) {
    for (let x = Math.floor(cx - r); x < Math.ceil(cx + r); x++) {
      if (Math.hypot(x + 0.5 - cx, y + 0.5 - cy) > r) continue
      blend(y * N + x, color)
    }
  }
}
function triangle(ax, ay, bx, by, cx, cy, color) {
  const minX = Math.floor(Math.min(ax, bx, cx)), maxX = Math.ceil(Math.max(ax, bx, cx))
  const minY = Math.floor(Math.min(ay, by, cy)), maxY = Math.ceil(Math.max(ay, by, cy))
  const sign = (px, py, qx, qy, rx, ry) => (px - rx) * (qy - ry) - (qx - rx) * (py - ry)
  for (let y = minY; y < maxY; y++) {
    for (let x = minX; x < maxX; x++) {
      const p = [x + 0.5, y + 0.5]
      const d1 = sign(p[0], p[1], ax, ay, bx, by)
      const d2 = sign(p[0], p[1], bx, by, cx, cy)
      const d3 = sign(p[0], p[1], cx, cy, ax, ay)
      const neg = d1 < 0 || d2 < 0 || d3 < 0
      const pos = d1 > 0 || d2 > 0 || d3 > 0
      if (neg && pos) continue
      blend(y * N + x, color)
    }
  }
}

const u = (v) => v * S // 逻辑坐标 → 超采样坐标

// 底:深色圆角方块 + 一圈描边
roundRect(u(6), u(6), u(250), u(250), u(52), C.bg)

// 左侧槽位:断点红点 + 停站琥珀箭头(与编辑器行号槽一致)
circle(u(52), u(78), u(13), C.bp)
triangle(u(42), u(116), u(42), u(152), u(72), u(134), C.stop)

// 右侧代码行(宽度不一,颜色取主题语法色)
const bars = [
  [u(88), u(70), u(206), C.keyword],
  [u(88), u(100), u(224), C.plain],
  [u(112), u(130), u(232), C.comment],
  [u(88), u(160), u(200), C.string],
  [u(88), u(190), u(214), C.plain],
]
for (const [x0, y0, x1, color] of bars) roundRect(x0, y0, x1, y0 + u(13), u(6.5), color)

// ---- 降采样(按 alpha 加权,避免圆角边缘发黑) ----
const out = Buffer.alloc(W * W * 4)
for (let y = 0; y < W; y++) {
  for (let x = 0; x < W; x++) {
    let r = 0, g = 0, b = 0, a = 0
    for (let sy = 0; sy < S; sy++) {
      for (let sx = 0; sx < S; sx++) {
        const o = ((y * S + sy) * N + (x * S + sx)) * 4
        const al = buf[o + 3] / 255
        r += buf[o] * al; g += buf[o + 1] * al; b += buf[o + 2] * al; a += al
      }
    }
    const n = S * S
    const o = (y * W + x) * 4
    if (a > 0) { out[o] = Math.round(r / a); out[o + 1] = Math.round(g / a); out[o + 2] = Math.round(b / a) }
    out[o + 3] = Math.round((a / n) * 255)
  }
}

// ---- PNG 编码(RGBA8,filter 0) ----
function chunk(type, data) {
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data])
  const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(body) >>> 0)
  return Buffer.concat([len, body, crc])
}
const ihdr = Buffer.alloc(13)
ihdr.writeUInt32BE(W, 0); ihdr.writeUInt32BE(W, 4); ihdr[8] = 8; ihdr[9] = 6
const raw = Buffer.concat(Array.from({ length: W }, (_, y) => Buffer.concat([Buffer.from([0]), out.subarray(y * W * 4, (y + 1) * W * 4)])))
const png = Buffer.concat([
  Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
  chunk('IHDR', ihdr), chunk('IDAT', deflateSync(raw, { level: 9 })), chunk('IEND', Buffer.alloc(0)),
])

// ---- ICO 容器(Windows Vista+ 支持内嵌 PNG;256×256 用宽高 0 表示) ----
const ico = Buffer.alloc(6 + 16)
ico.writeUInt16LE(0, 0); ico.writeUInt16LE(1, 2); ico.writeUInt16LE(1, 4) // reserved/type/count
ico[6] = 0; ico[7] = 0; ico[8] = 0; ico[9] = 0                // 0 = 256
ico.writeUInt16LE(1, 10); ico.writeUInt16LE(32, 12)          // planes / bpp
ico.writeUInt32LE(png.length, 14)
ico.writeUInt32LE(22, 18)

fs.mkdirSync(outDir, { recursive: true })
const pngPath = path.join(outDir, 'icon.png')
const icoPath = path.join(outDir, 'icon.ico')
fs.writeFileSync(pngPath, png)
fs.writeFileSync(icoPath, Buffer.concat([ico, png]))
console.log(`[icon] 生成 ${pngPath} (${png.length} B) 与 ${icoPath} (${ico.length + png.length} B)`)
