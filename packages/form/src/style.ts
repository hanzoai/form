// The renderer's one stylesheet, scoped under .hzform and parameterised by the
// custom properties form.tsx sets from the theme: --hzform-question, -answer,
// -button, -ink (the button's label), -bg and -image. --hzform-opacity is not
// set here: a host (createWidget) sets it on the container and it is
// inherited, so the background alone fades. Without a theme font the form takes
// the page's.

export const css: string = `
.hzform{position:relative;box-sizing:border-box;height:100%;min-height:inherit;color:var(--hzform-question);font-size:16px;line-height:1.5;-webkit-text-size-adjust:100%;text-align:start}
.hzform *,.hzform *::before,.hzform *::after{box-sizing:border-box}
.hzform .layer{position:absolute;inset:0;background-color:var(--hzform-bg);background-image:var(--hzform-image);background-size:cover;background-position:center;opacity:var(--hzform-opacity,1);pointer-events:none}
.hzform .scroll{position:relative;display:flex;flex-direction:column;height:100%;min-height:inherit;overflow-x:hidden;overflow-y:auto}
.hzform form{display:flex;flex:1;flex-direction:column;width:100%;margin:0}
.hzform .head{margin:0;padding:16px 16px 0;font-size:15px;font-weight:600;opacity:.8;overflow-wrap:anywhere}
.hzform .body{display:flex;flex:1;flex-direction:column;justify-content:center;width:100%;max-width:720px;margin:0 auto;padding:32px 16px}
.hzform[data-mode=all] .body{justify-content:flex-start;gap:48px}
.hzform .q{display:flex;flex-direction:column;min-width:0}
.hzform[data-mode=one] .q,.hzform .screen{animation:hzform-in .24s ease-out}
.hzform fieldset{min-width:0;margin:0;padding:0;border:0}
.hzform legend{padding:0}
.hzform .title{display:block;margin:0;color:var(--hzform-question);font-size:24px;font-weight:500;line-height:1.3;overflow-wrap:anywhere}
.hzform .desc{margin:6px 0 0;font-size:16px;opacity:.75;white-space:pre-line;overflow-wrap:anywhere}
.hzform .text{display:block;width:100%;min-height:48px;margin:16px 0 0;padding:8px 2px;border:0;border-bottom:1px solid color-mix(in srgb,var(--hzform-answer) 45%,transparent);border-radius:0;background:transparent;color:var(--hzform-answer);font:inherit;font-size:20px}
.hzform textarea.text{min-height:96px;resize:vertical}
.hzform select.text{cursor:pointer}
.hzform select.text option{background:var(--hzform-bg);color:var(--hzform-answer)}
.hzform .text::placeholder{color:var(--hzform-answer);opacity:.45}
.hzform :focus-visible{outline:2px solid var(--hzform-answer);outline-offset:3px}
.hzform .tiles{display:flex;flex-direction:column;gap:8px;max-width:480px;margin-top:16px}
.hzform .tile{position:relative;display:flex;align-items:center;gap:12px;min-height:48px;padding:8px 12px;border:1px solid color-mix(in srgb,var(--hzform-answer) 50%,transparent);border-radius:6px;background:color-mix(in srgb,var(--hzform-answer) 8%,transparent);color:var(--hzform-answer);font-size:18px;cursor:pointer;overflow-wrap:anywhere}
.hzform .tile:hover,.hzform .cell:hover{background:color-mix(in srgb,var(--hzform-answer) 16%,transparent)}
.hzform .tile[data-on=true],.hzform .cell[data-on=true]{border-color:var(--hzform-answer);background:color-mix(in srgb,var(--hzform-answer) 26%,transparent)}
.hzform .tile[data-on=true]::after{content:"\\2713";margin-inline-start:auto;font-weight:700}
.hzform .tile input,.hzform .cell input,.hzform .star input{position:absolute;width:1px;height:1px;margin:0;opacity:0;pointer-events:none}
.hzform .tile:has(input:focus-visible),.hzform .cell:has(input:focus-visible),.hzform .star:has(input:focus-visible){outline:2px solid var(--hzform-answer);outline-offset:3px}
.hzform .key{display:inline-flex;flex:none;align-items:center;justify-content:center;min-width:24px;height:24px;padding:0 6px;border:1px solid currentColor;border-radius:4px;font-size:12px;font-weight:600}
.hzform [data-on=true] .key{background:var(--hzform-answer);color:var(--hzform-bg)}
.hzform .stars{display:flex;flex-wrap:wrap;gap:4px;margin-top:16px}
.hzform .star{position:relative;display:flex;flex-direction:column;align-items:center;justify-content:center;min-width:44px;min-height:56px;padding:4px;border-radius:6px;color:var(--hzform-answer);cursor:pointer}
.hzform .star svg{width:32px;height:32px;fill:none;stroke:currentColor;stroke-width:1.5;stroke-linejoin:round}
.hzform .star[data-on=true] svg{fill:currentColor}
.hzform .star .n{font-size:12px;opacity:.75}
.hzform .scale{display:grid;grid-template-columns:repeat(auto-fit,minmax(44px,1fr));gap:4px;margin-top:16px}
.hzform .cell{position:relative;display:flex;align-items:center;justify-content:center;min-height:48px;border:1px solid color-mix(in srgb,var(--hzform-answer) 50%,transparent);border-radius:6px;background:color-mix(in srgb,var(--hzform-answer) 8%,transparent);color:var(--hzform-answer);font-size:18px;cursor:pointer}
.hzform .ends{display:flex;justify-content:space-between;gap:8px;margin-top:8px;font-size:14px;opacity:.8}
.hzform .problem,.hzform .failure{margin:8px 0 0;font-size:15px}
.hzform .problem:not(:empty),.hzform .failure:not(:empty){padding:6px 10px;border-inline-start:3px solid #ef4444;border-radius:4px;background:color-mix(in srgb,#ef4444 18%,transparent)}
.hzform .actions{display:flex;flex-wrap:wrap;align-items:center;gap:12px;margin-top:24px}
.hzform .go{display:inline-flex;align-items:center;justify-content:center;min-width:88px;min-height:48px;padding:0 20px;border:0;border-radius:6px;background:var(--hzform-button);color:var(--hzform-ink);font:inherit;font-size:18px;font-weight:600;text-decoration:none;cursor:pointer}
.hzform .go:hover{filter:brightness(1.08)}
.hzform .go[aria-disabled=true]{opacity:.6;cursor:progress}
.hzform .hint{font-size:13px;opacity:.7}
.hzform .screen{display:flex;flex-direction:column;align-items:flex-start;gap:16px}
.hzform .screen h2{margin:0;font-size:32px;font-weight:600;line-height:1.2;overflow-wrap:anywhere}
.hzform .screen p{margin:0;font-size:18px;opacity:.85;white-space:pre-line;overflow-wrap:anywhere}
.hzform .foot{display:flex;align-items:center;gap:12px;padding:12px 16px}
.hzform .bar{flex:1;height:4px;overflow:hidden;border-radius:2px;background:color-mix(in srgb,var(--hzform-question) 20%,transparent)}
.hzform .bar>span{display:block;height:100%;background:var(--hzform-question);transition:width .2s}
.hzform .nav{display:flex;gap:4px;margin-inline-start:auto}
.hzform .nav button{display:inline-flex;align-items:center;justify-content:center;width:44px;height:44px;padding:0;border:0;border-radius:6px;background:var(--hzform-button);color:var(--hzform-ink);cursor:pointer}
.hzform .nav button:disabled{opacity:.4;cursor:default}
.hzform .nav svg{width:20px;height:20px;fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}
.hzform .sr,.hzform .trap{position:absolute;width:1px;height:1px;margin:-1px;padding:0;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap;border:0}
@media (max-width:640px){.hzform .title{font-size:20px}.hzform .screen h2{font-size:26px}}
@media (pointer:coarse){.hzform .hint{display:none}}
@media (prefers-reduced-motion:reduce){.hzform[data-mode=one] .q,.hzform .screen{animation:none}.hzform .bar>span{transition:none}}
@keyframes hzform-in{from{opacity:0;transform:translateY(12px)}to{opacity:1;transform:none}}
`
