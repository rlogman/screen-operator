import sys, json, yaml, jsonschema
def merge(a, b):
    if isinstance(a, dict) and isinstance(b, dict):
        out = dict(a)
        for k, v in b.items():
            out[k] = merge(a[k], v) if k in a else v
        return out
    return b
cfg = {}
for p in sys.argv[2:]:
    cfg = merge(cfg, yaml.safe_load(open(p)))
schema = json.load(open(sys.argv[1]))
jsonschema.Draft202012Validator.check_schema(schema)
errs = list(jsonschema.Draft202012Validator(schema).iter_errors(cfg))
for e in errs: print("ERROR", list(e.path), e.message)
print("OK" if not errs else f"{len(errs)} errores")
