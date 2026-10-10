#!/usr/bin/env bash
# Ejecuta un comando con acceso exclusivo al escritorio de pruebas.
# Uso: scripts/with_desktop.sh <comando> [args...]
# El lock es un directorio (mkdir es atómico). Espera hasta 15 min y libera al salir.
set -euo pipefail
LOCK="/Users/rlogman/projects/frontier-hackathon/screen-operator/.artifex/desktop.lock"
for i in $(seq 1 900); do
  if mkdir "$LOCK" 2>/dev/null; then
    echo "$$ $(date +%H:%M:%S) $*" > "$LOCK/owner"
    trap 'rm -rf "$LOCK"' EXIT
    "$@"
    exit $?
  fi
  # Lock huérfano: solo por antigüedad. No usar kill -0: dentro del sandbox no ve
  # los procesos de otros agentes y daría por muerto a un dueño vivo.
  age=$(( $(date +%s) - $(stat -f %m "$LOCK" 2>/dev/null || date +%s) ))
  if [ "$age" -gt 1800 ]; then
    echo "lock con más de 30 min ($(cat "$LOCK/owner" 2>/dev/null)): se libera" >&2
    rm -rf "$LOCK"
    continue
  fi
  [ $((i % 30)) -eq 1 ] && echo "esperando el escritorio (lo usa: $(cat "$LOCK/owner" 2>/dev/null || echo '?'))" >&2
  sleep 1
done
echo "timeout esperando el lock del escritorio" >&2
exit 3
