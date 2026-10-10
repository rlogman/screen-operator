import base64, pathlib, re
d = pathlib.Path(__file__).resolve().parent
t = (d / "template.html").read_text()
names = {"C4_1": "c4-1-contexto", "C4_2": "c4-2-contenedores", "C4_3": "c4-3-componentes", "C4_4": "c4-4-dinamico"}
for k, n in names.items():
    b64 = base64.b64encode((d.parent / "diagrams" / f"{n}.png").read_bytes()).decode()
    t = t.replace('<pre class="mermaid">{{%s}}</pre>' % k, f'<img alt="{n}" src="data:image/png;base64,{b64}" style="width:100%;display:block">')
assert "{{" not in t
t = t.replace("</style>", """
@page{size:A4;margin:14mm 12mm}
html{-webkit-print-color-adjust:exact;print-color-adjust:exact}
body{font-size:12.5px}
.wrap{max-width:none;padding-inline:0}
.titlebar{padding:8px 0}
table{min-width:0;font-size:11.5px}
tr,.diagram,.grid2>div,pre.code{break-inside:avoid}
h2,h3{break-after:avoid}
section{padding-top:18px}
nav.toc{display:none}
</style>""", 1)
out = d / "screen-operator-diseno.print.html"
out.write_text('<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">' + t + "</html>")
print(out)
