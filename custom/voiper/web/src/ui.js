// Shared Tailwind class lists. Pages use these names instead of repeating long
// utility strings; colors come from the theme tokens in app.css.
//
// Some lists keep a plain marker class (panel, row, stack) because parents
// space their children with selectors such as [&>.row+.row].

const control = `
  cursor-pointer outline-offset-3 leading-[1.4] transition-colors duration-150
  disabled:cursor-not-allowed disabled:opacity-[0.45]
  enabled:active:shadow-[inset_0_1px_3px_#0004]
`;

const button = `
  ${control} min-h-[38px] px-4 py-[0.65rem] rounded-[6px] border font-medium
`;

const input = `
  min-w-0 w-full min-h-[42px] rounded-[6px] border border-field-border bg-field text-foreground
  outline-offset-3 placeholder:text-[#858d99]
  focus:border-transparent focus:outline-2 focus:outline-solid focus:outline-accent
  file:mr-3 file:cursor-pointer file:rounded-[4px] file:border-0 file:bg-control-hover
  file:px-3 file:py-2 file:text-inherit [&[type='file']]:p-2
`;

// Card container. A leading h2 becomes the card header with a divider, and
// stacked buttons and rows get breathing room.
const card = `
  panel min-w-0 rounded-[6px] border border-border border-t-panel-edge bg-surface p-6 shadow-panel
  max-[700px]:p-[1.15rem]
  [&>h2:first-child]:pb-4 [&>h2:first-child]:border-b [&>h2:first-child]:border-border
  [&>button+button]:mt-4 [&>.row+.row]:mt-4
  [&_form+details_button]:m-0 [&_form+details_button]:mt-2 [&_form+details_button]:mr-2
`;

export const ui = {
  control,

  button: `
    ${button} bg-control text-inherit border-control-border shadow-control
    enabled:hover:bg-control-hover enabled:hover:border-control-border-hover
  `,
  primaryButton: `
    ${button} primary bg-brand text-white font-semibold border-brand-border shadow-control
    enabled:hover:bg-brand-hover
  `,
  quietButton: `
    ${button} quiet bg-transparent text-muted border-control-border
    enabled:hover:bg-control-hover enabled:hover:border-control-border-hover
  `,
  dangerButton: `
    ${button} danger bg-[#382027] text-[#ffc2cc] border-[#70414c] shadow-control
    enabled:hover:bg-control-hover enabled:hover:border-control-border-hover
  `,
  // Square, borderless button holding a single 18px icon.
  iconButton: `
    ${control} inline-flex items-center justify-center size-[32px] min-h-[32px] p-[6px] rounded-[4px]
    border border-transparent bg-transparent text-muted font-medium
    enabled:hover:bg-control-hover enabled:hover:border-control-border-hover
    [&.active]:bg-accent-surface [&.active]:border-brand-border [&.active]:text-accent-text
    [&_svg]:size-[18px] [&_svg]:fill-none [&_svg]:stroke-current [&_svg]:stroke-[1.6]
    [&_svg]:[stroke-linecap:round] [&_svg]:[stroke-linejoin:round]
  `,

  input,
  // Text input with the standard inner spacing.
  field: `${input} px-[0.8rem] py-[0.65rem]`,
  textarea: `${input} min-h-[90px] px-[0.8rem] py-[0.65rem] resize-y`,
  checkbox: `
    appearance-none size-[20px] min-h-[20px] flex-[0_0_20px] m-0 p-0 rounded-[4px] border border-[#697583]
    bg-field bg-center bg-no-repeat bg-size-[14px_14px] shadow-none cursor-pointer
    transition-colors duration-150
    checked:border-accent checked:bg-brand indeterminate:border-accent indeterminate:bg-brand
    enabled:hover:border-accent-text focus:border-accent focus:outline-none
    focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-accent focus-visible:outline-offset-3
    disabled:cursor-not-allowed disabled:opacity-40
  `,
  range: `
    appearance-none w-full min-w-0 min-h-[28px] p-0 border-0 bg-transparent cursor-pointer outline-offset-3
    focus:outline-2 focus:outline-solid focus:outline-accent
    disabled:cursor-not-allowed disabled:opacity-40
  `,

  // A label stacked above its control.
  label: "flex flex-col gap-2 text-[0.9rem] text-label",
  // A label beside a checkbox or radio button.
  checkLabel: "flex flex-row items-center gap-[0.65rem] min-h-[32px] text-[0.9rem] text-label cursor-pointer",

  text: "m-0 leading-[1.6]",
  hint: "m-0 leading-[1.6] text-[0.78rem] text-muted",
  error: "m-0 leading-[1.6] text-danger",
  empty: "m-0 px-0 py-8 leading-[1.6] text-center text-muted",
  badge: "px-[0.7rem] py-[0.3rem] rounded-[4px] bg-control-hover text-xs",

  h2: "mx-0 mt-0 mb-4 text-[1.15rem] font-semibold",
  h3: "m-0 text-base font-semibold",

  card,
  // Card whose direct paragraphs are spaced apart.
  panel: `${card} [&>p]:mb-4`,
  // Controls in a line. Buttons keep their width; on narrow windows the line
  // wraps and elements marked .grow take the remaining space.
  row: "row flex items-center gap-[0.7rem] max-[700px]:flex-wrap [&>button]:shrink-0 max-[700px]:[&>.grow]:basis-[180px]",
  stack: "stack flex flex-col gap-4",

  details: `
    [&>label]:mt-4 [&>form]:mt-4 [&>fieldset]:mt-4 [&>button]:mr-2 [&>.row>button]:shrink-0
    [&>:not(summary)+:not(summary)]:mt-4 [&:not([open])>summary]:mb-0
  `,
  summary: "list-none mb-[0.8rem] py-1 text-[#d1dce5] cursor-pointer outline-offset-3",
  // Bordered disclosure section inside a panel.
  section: "mt-[1.3rem] pt-4 border-t border-border",
};
