#!/usr/bin/env bash
# Verifica el entrypoint desplegado contra el contrato del evento (historia 4.2).
#
# Uso: scripts/check_entrypoint.sh <url-base> <token>
#   scripts/check_entrypoint.sh https://screen-operator-production.up.railway.app "$TEAM_TOKEN"
#
# Chequeos:
#   1. GET /health con el token responde 200.
#   2. POST /tasks sin token responde 401 (el agente no acepta tareas anonimas).
#   3. POST /tasks con el token responde 200 con {"task_id": "..."} en menos de 2 s.
#   4. GET /tasks/{task_id} responde 200 con "estado" (en_curso|terminada|fallida) y "resumen".
#
# La tarea de prueba apunta por defecto a un escritorio inexistente
# (wss://escritorio.invalid/websockify), asi que no toca ningun escritorio real
# y termina "fallida". Variables opcionales:
#   CHECK_DESKTOP_URL, CHECK_DESKTOP_PASSWORD, CHECK_TASK, CHECK_MAX_POST_S (2), CURL (curl)
#
# El token nunca se imprime ni va en la linea de comandos de curl: viaja en un
# archivo de cabeceras temporal con permisos 600 que se borra al salir.
# Sale con 0 si todo pasa, 1 si algun chequeo falla, 2 por uso incorrecto.
# Requiere: curl, python3.

set -u

usage() {
	echo "uso: $0 <url-base> <token>" >&2
	exit 2
}

[ $# -eq 2 ] || usage
base="${1%/}"
token="$2"
case "$base" in
http://* | https://*) ;;
*)
	echo "check_entrypoint: la URL debe empezar con http:// o https://" >&2
	exit 2
	;;
esac
[ -n "$token" ] || {
	echo "check_entrypoint: el token esta vacio" >&2
	exit 2
}
command -v python3 >/dev/null 2>&1 || {
	echo "check_entrypoint: falta python3" >&2
	exit 2
}

CURL="${CURL:-curl}"
max_post_s="${CHECK_MAX_POST_S:-2}"
desktop_url="${CHECK_DESKTOP_URL:-wss://escritorio.invalid/websockify}"
desktop_password="${CHECK_DESKTOP_PASSWORD:-no-es-un-secreto}"
task="${CHECK_TASK:-Verificacion del entrypoint: no hagas ningun cambio y reporta la tarea como fallida.}"

umask 077
tmp="$(mktemp -d "${TMPDIR:-/tmp}/check_entrypoint.XXXXXX")" || exit 2
trap 'rm -rf "$tmp"' EXIT
printf 'Authorization: Bearer %s\n' "$token" >"$tmp/auth"
unset token

fails=0
ok() { echo "OK    $*"; }
bad() {
	echo "FALLA $*"
	fails=$((fails + 1))
}

# req <salida> <metodo> <ruta> [curl args...] -> imprime "<http_code> <time_total>"
req() {
	local out="$1" method="$2" path="$3"
	shift 3
	"$CURL" -sS --max-time 15 -o "$out" -w '%{http_code} %{time_total}' -X "$method" "$@" "$base$path" 2>"$tmp/curl.err" ||
		echo "000 0"
}

# json_get <archivo> <clave> -> valor si es texto no vacio; sale != 0 si no.
json_get() {
	python3 - "$1" "$2" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as f:
        v = json.load(f)
except Exception:
    sys.exit(1)
x = v.get(sys.argv[2]) if isinstance(v, dict) else None
if not isinstance(x, str):
    sys.exit(1)
print(x)
PY
}

echo "Entrypoint: $base"

# 1. /health
read -r code _ <<<"$(req "$tmp/health" GET /health -H @"$tmp/auth")"
if [ "$code" = "200" ]; then
	ok "GET /health -> 200"
else
	bad "GET /health -> $code (se esperaba 200) $(head -c 200 "$tmp/curl.err" 2>/dev/null)"
fi

# Cuerpo de la tarea de prueba, armado con python3 para escapar bien el JSON.
python3 - "$task" "$desktop_url" "$desktop_password" >"$tmp/body.json" <<'PY'
import json, sys
print(json.dumps({"tarea": sys.argv[1], "escritorio": {"url": sys.argv[2], "password": sys.argv[3]}}, ensure_ascii=False))
PY

# 2. POST /tasks sin token
read -r code _ <<<"$(req "$tmp/noauth" POST /tasks -H 'Content-Type: application/json' --data-binary @"$tmp/body.json")"
if [ "$code" = "401" ]; then
	ok "POST /tasks sin token -> 401"
else
	bad "POST /tasks sin token -> $code (se esperaba 401)"
fi

# 3. POST /tasks con token: task_id en menos de max_post_s
read -r code secs <<<"$(req "$tmp/create" POST /tasks -H @"$tmp/auth" -H 'Content-Type: application/json' --data-binary @"$tmp/body.json")"
task_id=""
if [ "$code" != "200" ]; then
	bad "POST /tasks -> $code (se esperaba 200) $(head -c 200 "$tmp/create" 2>/dev/null)"
elif ! task_id="$(json_get "$tmp/create" task_id)" || [ -z "$task_id" ]; then
	task_id=""
	bad "POST /tasks -> 200 pero sin task_id de texto: $(head -c 200 "$tmp/create")"
elif ! python3 -c 'import sys; sys.exit(0 if float(sys.argv[1]) < float(sys.argv[2]) else 1)' "$secs" "$max_post_s"; then
	bad "POST /tasks tardo ${secs}s (limite ${max_post_s}s)"
else
	ok "POST /tasks -> 200 task_id=$task_id en ${secs}s"
fi

# 4. GET /tasks/{task_id}
if [ -z "$task_id" ]; then
	bad "GET /tasks/{task_id} no se puede probar sin task_id"
elif ! printf '%s' "$task_id" | grep -Eq '^[A-Za-z0-9._~-]+$'; then
	bad "task_id con caracteres no seguros para una URL: $task_id"
else
	read -r code _ <<<"$(req "$tmp/get" GET "/tasks/$task_id" -H @"$tmp/auth")"
	estado="$(json_get "$tmp/get" estado)" || estado=""
	if [ "$code" != "200" ]; then
		bad "GET /tasks/$task_id -> $code (se esperaba 200) $(head -c 200 "$tmp/get" 2>/dev/null)"
	elif ! resumen="$(json_get "$tmp/get" resumen)"; then
		bad "GET /tasks/$task_id sin \"resumen\" de texto: $(head -c 200 "$tmp/get")"
	else
		case "$estado" in
		en_curso | terminada | fallida)
			ok "GET /tasks/$task_id -> estado=$estado resumen=\"$(printf '%s' "$resumen" | head -c 120)\""
			;;
		*)
			bad "GET /tasks/$task_id -> estado=\"$estado\" (se esperaba en_curso, terminada o fallida)"
			;;
		esac
	fi
fi

if [ "$fails" -eq 0 ]; then
	echo "RESULTADO: entrypoint OK"
	exit 0
fi
echo "RESULTADO: $fails chequeo(s) fallaron"
exit 1
