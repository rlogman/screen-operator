import html, pathlib
d = pathlib.Path(__file__).resolve().parent
t = (d / "template.html").read_text()
names = {"C4_1": "c4-1-contexto", "C4_2": "c4-2-contenedores", "C4_3": "c4-3-componentes", "C4_4": "c4-4-dinamico"}
for k, n in names.items():
    t = t.replace("{{%s}}" % k, html.escape((d.parent / "diagrams" / f"{n}.mmd").read_text().strip(), quote=False))
assert "{{" not in t
(d / "screen-operator-diseno.html").write_text(t)
print("ok", len(t))
