// shadcn/ui 规范基础组件:语义 token + cn/cva,颜色一律不写死(亮暗由 index.css token 决定)
import * as React from 'react'
import * as AlertDialogPrimitive from '@radix-ui/react-alert-dialog'
import * as CheckboxPrimitive from '@radix-ui/react-checkbox'
import * as PopoverPrimitive from '@radix-ui/react-popover'
import * as SelectPrimitive from '@radix-ui/react-select'
import { Slot } from '@radix-ui/react-slot'
import { DayPicker } from 'react-day-picker'
import { CalendarDays, Check, ChevronDown, ChevronLeft, ChevronRight } from 'lucide-react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from './lib/utils'

const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap text-sm font-medium transition-colors outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] disabled:pointer-events-none disabled:opacity-40 [&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground hover:bg-primary/90',
        destructive: 'bg-destructive text-white hover:bg-destructive/90',
        outline: 'border border-input bg-transparent hover:bg-accent hover:text-accent-foreground',
        secondary: 'bg-secondary text-secondary-foreground hover:bg-secondary/80',
        ghost: 'hover:bg-accent hover:text-accent-foreground',
        link: 'text-primary underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-8 px-3',
        sm: 'h-7 gap-1 px-2 text-xs',
        lg: 'h-10 px-6',
        icon: 'h-8 w-8',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)

// 转发 ref:作为 Radix 的 asChild 子元素时(如 PopoverTrigger)需要真实 DOM 引用做定位锚点
export const Button = React.forwardRef<HTMLButtonElement,
  React.ButtonHTMLAttributes<HTMLButtonElement> &
  VariantProps<typeof buttonVariants> & { asChild?: boolean }
>(function Button({ className, variant, size, asChild = false, ...props }, ref) {
  const Comp = asChild ? Slot : 'button'
  return <Comp ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
})

const badgeVariants = cva(
  'inline-flex items-center border px-1.5 py-0.5 text-[11px] font-medium',
  {
    variants: {
      tone: {
        default: 'border-transparent bg-secondary text-secondary-foreground',
        green: 'border-emerald-500/20 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
        yellow: 'border-yellow-500/20 bg-yellow-500/10 text-yellow-600 dark:text-yellow-400',
        red: 'border-red-500/20 bg-red-500/10 text-red-600 dark:text-red-400',
        blue: 'border-sky-500/20 bg-sky-500/10 text-sky-600 dark:text-sky-400',
        gray: 'border-transparent bg-muted text-muted-foreground',
      },
    },
    defaultVariants: { tone: 'default' },
  },
)

export function Badge({ className, tone, ...props }:
  React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />
}

export function Panel({ title, right, children, className }: {
  title: React.ReactNode; right?: React.ReactNode; children: React.ReactNode; className?: string
}) {
  return (
    <div className={cn('flex min-h-0 flex-col border border-border bg-card', className)}>
      <div className="flex h-8 shrink-0 items-center justify-between border-b border-border px-2.5">
        <span className="text-xs font-medium text-muted-foreground">{title}</span>
        {right}
      </div>
      <div className="min-h-0 flex-1 overflow-auto">{children}</div>
    </div>
  )
}

export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  function Input({ className, type, ...props }, ref) {
    return (
      <input
        ref={ref}
        type={type}
        className={cn(
          'flex h-8 w-full min-w-0 border border-input bg-transparent px-2 py-1 text-sm transition-[color,box-shadow] outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30',
          className,
        )}
        {...props}
      />
    )
  },
)

export function Separator({ className, orientation = 'horizontal' }: {
  className?: string; orientation?: 'horizontal' | 'vertical'
}) {
  return (
    <div
      role="separator"
      className={cn(
        'shrink-0 bg-border',
        orientation === 'horizontal' ? 'h-px w-full' : 'h-full w-px',
        className,
      )}
    />
  )
}

// ---- Select(shadcn 规范,Radix 实现):全站下拉统一用这套,不再用原生 <select> ----
// 触发钮外观对齐 Input(同高同边框同聚焦环),尺寸/宽度由调用方 className 覆盖(cn 后写优先)。
export function Select(props: React.ComponentProps<typeof SelectPrimitive.Root>) {
  return <SelectPrimitive.Root {...props} />
}

export function SelectTrigger({ className, children, ...props }:
  React.ComponentProps<typeof SelectPrimitive.Trigger>) {
  return (
    <SelectPrimitive.Trigger
      className={cn(
        'flex h-8 w-fit min-w-0 items-center justify-between gap-1.5 border border-input bg-transparent px-2 py-1 text-sm whitespace-nowrap transition-[color,box-shadow] outline-none',
        'data-[placeholder]:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]',
        'disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30 [&>span]:truncate',
        className,
      )}
      {...props}
    >
      {children}
      <SelectPrimitive.Icon asChild>
        <ChevronDown className="size-4 shrink-0 opacity-50" />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  )
}

export function SelectValue(props: React.ComponentProps<typeof SelectPrimitive.Value>) {
  return <SelectPrimitive.Value {...props} />
}

export function SelectContent({ className, children, position = 'popper', ...props }:
  React.ComponentProps<typeof SelectPrimitive.Content>) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        position={position}
        sideOffset={4}
        className={cn(
          'relative z-50 max-h-[var(--radix-select-content-available-height)] min-w-28 overflow-y-auto overflow-x-hidden border border-border bg-popover text-popover-foreground shadow-md',
          position === 'popper' && 'w-full min-w-[var(--radix-select-trigger-width)]',
          className,
        )}
        {...props}
      >
        <SelectPrimitive.Viewport className="p-1">{children}</SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  )
}

export function SelectItem({ className, children, ...props }:
  React.ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      className={cn(
        'relative flex w-full cursor-default items-center py-1 pr-6 pl-2 text-sm outline-none select-none',
        'focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
        className,
      )}
      {...props}
    >
      <span className="absolute right-1.5 flex size-3.5 items-center justify-center">
        <SelectPrimitive.ItemIndicator>
          <Check className="size-3.5 text-primary" />
        </SelectPrimitive.ItemIndicator>
      </span>
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
    </SelectPrimitive.Item>
  )
}

// ---- Popover(shadcn 规范,Radix 实现):弹层底色/边框走 popover 语义 token ----
export function Popover(props: React.ComponentProps<typeof PopoverPrimitive.Root>) {
  return <PopoverPrimitive.Root {...props} />
}

export function PopoverTrigger(props: React.ComponentProps<typeof PopoverPrimitive.Trigger>) {
  return <PopoverPrimitive.Trigger {...props} />
}

export function PopoverContent({ className, align = 'end', sideOffset = 4, ...props }:
  React.ComponentProps<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        align={align}
        sideOffset={sideOffset}
        className={cn('z-50 border border-border bg-popover p-0 text-popover-foreground shadow-md outline-none', className)}
        {...props}
      />
    </PopoverPrimitive.Portal>
  )
}

// ---- Checkbox(shadcn 规范,Radix 实现) ----
export function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      className={cn(
        'size-3.5 shrink-0 border border-input outline-none transition-colors',
        'data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground',
        'focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]',
        'disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator className="flex items-center justify-center text-current">
        <Check className="size-3" />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}

// ---- Calendar(shadcn 规范,react-day-picker + 语义 token 映射;零圆角) ----
export function Calendar({ className, classNames, ...props }: React.ComponentProps<typeof DayPicker>) {
  return (
    <DayPicker
      className={cn('p-2 text-xs', className)}
      weekStartsOn={1}
      formatters={{
        formatCaption: (d) => `${d.getFullYear()} 年 ${d.getMonth() + 1} 月`,
        formatWeekdayName: (d) => '日一二三四五六'[d.getDay()],
        formatDay: (d) => String(d.getDate()),
      }}
      labels={{ labelPrevious: () => '上个月', labelNext: () => '下个月' }}
      classNames={{
        months: 'relative flex flex-col',
        month: 'flex flex-col gap-2',
        month_caption: 'flex h-7 items-center justify-center',
        caption_label: 'text-xs font-medium text-foreground',
        nav: 'absolute inset-x-1 top-2 flex items-center justify-between',
        button_previous: 'flex h-6 w-6 items-center justify-center text-muted-foreground hover:bg-accent hover:text-foreground',
        button_next: 'flex h-6 w-6 items-center justify-center text-muted-foreground hover:bg-accent hover:text-foreground',
        month_grid: 'border-collapse',
        weekdays: 'flex',
        weekday: 'w-7 py-1 text-[11px] font-normal text-muted-foreground',
        weeks: 'flex flex-col',
        week: 'flex w-full',
        day: 'h-7 w-7 p-0 text-center',
        day_button: 'h-7 w-7 text-xs text-foreground hover:bg-accent hover:text-accent-foreground',
        selected: '[&>button]:bg-primary [&>button]:text-primary-foreground [&>button:hover]:bg-primary',
        today: '[&>button]:font-semibold [&>button]:text-primary',
        outside: '[&>button]:text-muted-foreground/40',
        disabled: 'opacity-30',
        hidden: 'invisible',
        ...classNames,
      }}
      components={{
        Chevron: ({ orientation }) => (
          orientation === 'left' ? <ChevronLeft className="size-3.5" /> : <ChevronRight className="size-3.5" />
        ),
      }}
      {...props}
    />
  )
}

const pad2 = (n: number) => String(n).padStart(2, '0')
/** Date → yyyy-MM-dd(与查询参数同格式) */
export const fmtYmd = (d: Date) => `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
/** yyyy-MM-dd → Date(非法/空返回 undefined) */
export const parseYmd = (s: string): Date | undefined => {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec((s || '').trim())
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : undefined
}

// ---- DatePicker(shadcn 规范):按钮显示当前日期,弹层为日历 ----
export function DatePicker({ value, onChange, className, title, placeholder = '选择日期' }: {
  value: string; onChange: (v: string) => void; className?: string; title?: string; placeholder?: string
}) {
  const [open, setOpen] = React.useState(false)
  const sel = parseYmd(value)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" title={title} aria-label={title || '选择日期'}
          className={cn('h-7 justify-start gap-1.5 px-2 font-normal', !sel && 'text-muted-foreground', className)}>
          <CalendarDays className="size-3.5" />
          {sel ? fmtYmd(sel) : placeholder}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-auto">
        <Calendar
          mode="single"
          selected={sel}
          defaultMonth={sel}
          onSelect={(d) => { if (d) { onChange(fmtYmd(d)); setOpen(false) } }}
        />
      </PopoverContent>
    </Popover>
  )
}

// ---- Table(shadcn 规范,对齐 TbmLite 的可编辑表格):单元格自带网格线,控件嵌在格内 ----
// 表格容器横向可滚;TableHead/TableCell 带 border 形成 Excel 式网格(零圆角由 index.css 统一)。
// container=false 供「需要表头 sticky」的列表使用:外层 overflow-x-auto 的 div 按 CSS 规则
// 会让 overflow-y 也算成非 visible,于是它成了新的滚动祖先,里层 th 的 sticky 就失效了
// (滚动容器改由调用方提供)。默认仍套容器,不影响既有网格表格。
export function Table({ className, container = true, ...props }:
  React.ComponentProps<'table'> & { container?: boolean }) {
  const table = <table className={cn('w-full caption-bottom border-collapse text-sm', className)} {...props} />
  if (!container) return table
  return <div className="relative w-full overflow-x-auto">{table}</div>
}

export function TableHeader({ className, ...props }: React.ComponentProps<'thead'>) {
  return <thead className={cn('', className)} {...props} />
}

export function TableBody({ className, ...props }: React.ComponentProps<'tbody'>) {
  return <tbody className={cn('', className)} {...props} />
}

export function TableRow({ className, ...props }: React.ComponentProps<'tr'>) {
  return <tr className={cn('transition-colors hover:bg-muted/50', className)} {...props} />
}

export function TableHead({ className, ...props }: React.ComponentProps<'th'>) {
  return (
    <th
      className={cn(
        'h-7 border border-border bg-muted/50 px-2 text-left align-middle text-[11px] font-medium whitespace-nowrap text-muted-foreground',
        className,
      )}
      {...props}
    />
  )
}

export function TableCell({ className, ...props }: React.ComponentProps<'td'>) {
  return (
    <td className={cn('border border-border p-0 align-middle whitespace-nowrap', className)} {...props} />
  )
}

// ---- AlertDialog(shadcn 规范,Radix 实现):二次确认弹窗(不用浏览器原生 confirm) ----
export function AlertDialog(props: React.ComponentProps<typeof AlertDialogPrimitive.Root>) {
  return <AlertDialogPrimitive.Root {...props} />
}

export function AlertDialogContent({ className, children, ...props }:
  React.ComponentProps<typeof AlertDialogPrimitive.Content>) {
  return (
    <AlertDialogPrimitive.Portal>
      <AlertDialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/50" />
      <AlertDialogPrimitive.Content
        className={cn(
          'fixed top-1/2 left-1/2 z-50 flex w-full max-w-sm -translate-x-1/2 -translate-y-1/2 flex-col gap-2 border border-border bg-card p-4 text-xs text-card-foreground shadow-lg outline-none',
          className,
        )}
        {...props}
      >
        {children}
      </AlertDialogPrimitive.Content>
    </AlertDialogPrimitive.Portal>
  )
}

export function AlertDialogHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('flex flex-col gap-1.5', className)} {...props} />
}

export function AlertDialogFooter({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('mt-1 flex items-center justify-end gap-2', className)} {...props} />
}

export function AlertDialogTitle({ className, ...props }:
  React.ComponentProps<typeof AlertDialogPrimitive.Title>) {
  return <AlertDialogPrimitive.Title className={cn('text-sm font-medium text-foreground', className)} {...props} />
}

export function AlertDialogDescription({ className, ...props }:
  React.ComponentProps<typeof AlertDialogPrimitive.Description>) {
  return <AlertDialogPrimitive.Description className={cn('text-xs leading-5 text-muted-foreground whitespace-pre-line', className)} {...props} />
}

export function AlertDialogAction({ className, ...props }:
  React.ComponentProps<typeof AlertDialogPrimitive.Action>) {
  return (
    <AlertDialogPrimitive.Action asChild>
      <Button variant="destructive" className={className} {...props} />
    </AlertDialogPrimitive.Action>
  )
}

export function AlertDialogCancel({ className, ...props }:
  React.ComponentProps<typeof AlertDialogPrimitive.Cancel>) {
  return (
    <AlertDialogPrimitive.Cancel asChild>
      <Button variant="outline" className={className} {...props} />
    </AlertDialogPrimitive.Cancel>
  )
}

export const stateTone = (s: string): 'green' | 'yellow' | 'red' | 'gray' | 'blue' => {
  if (s === 'stopped') return 'yellow'
  if (s === 'running') return 'green'
  if (s === 'exit') return 'red'
  if (s === 'loading') return 'blue'
  return 'gray'
}

export const stateLabel = (s: string) => {
  const m: Record<string, string> = { stopped: '已停站', running: '运行中', exit: '已退出', loading: '启动中' }
  return m[s] || '未连接'
}
