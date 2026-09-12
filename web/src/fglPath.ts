// 4GL 文件名归一化(断点/停站/源码三处文件名来自不同来源,必须归一后才能比较):
// fgldb 报的断点与停站文件是 `bsft001_wf.4gl`,也可能带目录(`asf/bsft001_wf.4gl`)或全路径;
// 而调试页启动预取的 sourceDVM 可能是 `${模块}_${程序}.4gl`。严格比较会漏匹配——红点要停站
// 一次才出现、断点列表标不出"当前停在这个断点"。这里统一归一到可比的程序主名(小写、无扩展名、
// 去掉模块前缀)。不做大小写/路径以外的心智猜测,匹配不上就是不同文件。
export function progKey(f: string, module?: string): string {
  let b = f.split(/[\\/]/).pop() || f
  b = b.replace(/\.4gl$/i, '')
  if (module && b.toLowerCase().startsWith(module.toLowerCase() + '_')) b = b.slice(module.length + 1)
  return b.toLowerCase()
}
