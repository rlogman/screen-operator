# Despliegue en Railway

Runbook para publicar el agente con el contrato del evento (`material/README.md`, "Qué entregas a
las 20:00"). Lo sigue un integrante del equipo con su cuenta de Railway. El repositorio trae todo lo
que hace falta: `Dockerfile`, `.dockerignore`, `railway.json` y `scripts/check_entrypoint.sh`.

Documentación de Railway consultada el 2026-10-10:

- Config as code (`railway.json`): https://docs.railway.com/reference/config-as-code
- Esquema del archivo: https://railway.com/railway.schema.json
- Healthchecks: https://docs.railway.com/reference/healthchecks
- Variables: https://docs.railway.com/guides/variables
- Logs: https://docs.railway.com/guides/logs
- Redeploy y rollback: https://docs.railway.com/guides/deployment-actions
- Dominio público: https://docs.railway.com/guides/public-networking
- Crear proyecto desde GitHub: https://docs.railway.com/quick-start

## Antes de empezar

- Acceso al repositorio en GitHub y a la cuenta de Railway del equipo.
- El token del equipo (`TEAM_TOKEN`) y la llave de OpenRouter (`OPENROUTER_API_KEY`). Se pegan solo
  en Railway. Nunca en el repositorio, en un overlay, en un ticket ni en el chat del equipo.
- Opcional, para probar la imagen en local: Docker.

```bash
docker build -t screen-operator .
docker run --rm -e TEAM_TOKEN=t0k -e PORT=8080 -p 8080:8080 screen-operator
# en otra terminal
scripts/check_entrypoint.sh http://localhost:8080 t0k
```

## 1. Crear el servicio desde el repositorio

1. En el dashboard de Railway, **New Project** → **GitHub repo**. Si la cuenta no está enlazada con
   GitHub, enlázala cuando lo pida.
2. Busca el repositorio `screen-operator` y elígelo.
3. Elige **Add variables** (no **Deploy Now**): así el primer deploy ya arranca con el token. Sin
   `TEAM_TOKEN` el agente termina con código 1 a propósito y el deploy falla.
4. Railway construye con el `Dockerfile` de la raíz. Lo fija `railway.json`
   (`"builder": "DOCKERFILE"`) y, además, Railway usa siempre un Dockerfile cuando lo encuentra.

La imagen (ver `Dockerfile`):

- Etapa de build `golang:1.27.1-alpine`, binario estático (`CGO_ENABLED=0`).
- Etapa final `gcr.io/distroless/static-debian12:nonroot`: sin shell, sin toolchain de Go, usuario
  nonroot, certificados CA incluidos.
- La configuración del repo queda en `/app/config` y el agente la lee por `SO_CONFIG_DIR`.
- Al arrancar valida toda la configuración; si una capa es inválida el contenedor termina con 1.

## 2. Variables

En el servicio, pestaña **Variables**: **New Variable** (una por una) o **RAW Editor** (pegar
`NOMBRE=valor` por línea). Cada cambio queda como cambio pendiente (staged): hay que revisarlo y
desplegarlo para que tenga efecto.

| Variable | Obligatoria | Valor | Qué pasa si falta |
|---|---|---|---|
| `TEAM_TOKEN` | Sí | El token Bearer del equipo. Es el que manda el evaluador en `Authorization: Bearer <token>` | El contenedor termina con código 1 al arrancar y el healthcheck falla |
| `OPENROUTER_API_KEY` | Sí (desde la épica 6) | La llave de OpenRouter del equipo | Mientras el agente sea provisional no se usa; con el agente completo, las tareas fallan |
| `PORT` | Recomendada | `8080` | Railway inyecta su propio `PORT` y el agente escucha ahí; fijarlo en `8080` deja el puerto del dominio público predecible |
| `SO_CONFIG_OVERLAY` | No | Ruta a un YAML dentro de la imagen, p. ej. `/app/config/overlays/railway.yaml` | Sin overlay se usa la configuración del repo tal cual |

`SO_CONFIG_DIR` ya viene fijada en la imagen (`/app/config`). No la cambies.

### Overlay opcional (`SO_CONFIG_OVERLAY`)

El overlay ajusta la configuración del despliegue sin tocar las capas base (ver `docs/config.md`).
En Railway no se montan archivos, así que el overlay viaja dentro de la imagen:

1. Crea `config/overlays/<nombre>.yaml` en el repo con solo las claves que cambian, por ejemplo:
   ```yaml
   budgets:
     task_deadline_s: 280
   ```
2. Valídalo en local antes de subirlo:
   `SO_CONFIG_OVERLAY=config/overlays/<nombre>.yaml go run ./cmd/agent -check-config`
3. Commit y push. El `COPY config/` del Dockerfile lo deja en `/app/config/overlays/<nombre>.yaml`.
4. En Railway, `SO_CONFIG_OVERLAY=/app/config/overlays/<nombre>.yaml` y despliega.

Un overlay que no existe o no valida hace que el agente termine con código 1 y el deploy falle con
el error en los logs (nombra la clave o la ruta). El overlay queda en el repo y en la imagen: nunca
pongas un secreto ahí.

## 3. Healthcheck en /health

- `railway.json` declara `"healthcheckPath": "/health"` y `"healthcheckTimeout": 60`.
- Railway llama a `/health` en el `PORT` del servicio al iniciar cada deploy, desde el host
  `healthcheck.railway.app`, y solo activa el deploy nuevo cuando recibe un 2xx. No lo vuelve a
  consultar después.
- `/health` responde 200 `{"ok":true}` sin token (los healthchecks de Railway no mandan cabeceras) y
  también con token, que es como lo llama el evaluador.
- **Verifica en el dashboard** que el healthcheck quedó en `/health`: en **Settings** del servicio,
  el campo del health endpoint. Railway marcó Config as Code (`railway.json`) como obsoleto: los
  servicios existentes lo siguen leyendo hasta el 2026-12-01, pero la documentación de
  Infrastructure as Code dice que los servicios nuevos ya no pueden adoptarlo. Si el campo está
  vacío, escribe `/health` a mano. El builder no depende de esto: Railway usa el `Dockerfile` cuando
  lo encuentra.

## 4. Dominio público

En **Settings** del servicio → **Networking** → **Public Networking** → **Generate Domain**. Si
pregunta el puerto, usa `8080` (el valor de `PORT`). La URL queda como
`https://<algo>.up.railway.app`: es la que se registra en el evento.

## 5. Ver logs

1. Abre el servicio y haz clic en el deploy que quieres revisar.
2. El panel muestra los **Deploy Logs** (salida del agente). Para la compilación de la imagen, la
   pestaña **Build Logs**.

El agente escribe una línea JSON por evento con `timestamp`, `level`, `msg`, `fase` y, si aplica,
`task_id`. Al arrancar bien se ve `"msg":"agente escuchando"` con `addr`. Los secretos
(`TEAM_TOKEN`, `OPENROUTER_API_KEY`, contraseñas del escritorio) salen como `[redactado]`.

Errores típicos al arrancar:

| En los logs | Causa | Arreglo |
|---|---|---|
| `falta la variable de entorno TEAM_TOKEN` | Variable sin definir | Paso 2 |
| `SO_CONFIG_OVERLAY=...: el archivo no existe` | Ruta del overlay mal escrita o archivo sin commit | Paso 2, overlay |
| `config: ...` con una ruta de clave | Clave desconocida o valor fuera de rango en una capa | Corregir el YAML y validarlo con `-check-config` |
| Healthcheck failed, sin línea `agente escuchando` | El proceso terminó antes de escuchar | Leer la primera línea de error de los Deploy Logs |

## 6. Redeploy y rollback

- **Mismo código otra vez** (p. ej. tras cambiar variables): pestaña **Deployments** → menú de tres
  puntos del deploy → opción de redeploy. Crea un deploy nuevo con el mismo código y configuración.
- **Último commit de la rama por defecto**: Command Palette (`CMD + K`) → **Deploy Latest Commit**.
- **Rollback**: menú de tres puntos de un deploy anterior exitoso → rollback y confirmar. Restaura la
  imagen y las variables de ese deploy.

Después de cada deploy, corre el paso 7.

## 7. Validar el entrypoint

### Con el script del repo

```bash
scripts/check_entrypoint.sh https://<algo>.up.railway.app "$TEAM_TOKEN"
```

Requiere `curl` y `python3`. Chequea:

1. `GET /health` con el token → 200.
2. `POST /tasks` sin token → 401.
3. `POST /tasks` con el token → 200 con `{"task_id": "…"}` en menos de 2 s.
4. `GET /tasks/{task_id}` → 200 con `estado` (`en_curso`, `terminada` o `fallida`) y `resumen`.

Sale con 0 si todo pasa y con 1 si algo falla, una línea `OK` o `FALLA` por chequeo. La tarea de
prueba apunta a `wss://escritorio.invalid/websockify`, un escritorio que no existe: no toca ningún
escritorio real y termina `fallida`. El token no se imprime ni aparece en la línea de comandos de
`curl` (va en un archivo temporal con permisos 600). Para que el token no quede en el historial de la
shell, cárgalo en la variable `TEAM_TOKEN` (p. ej. con `read -s TEAM_TOKEN`) y pásalo como
`"$TEAM_TOKEN"`.

### Con el botón "VALIDAR ENTRYPOINT" del evento

1. En la página del evento (`/evento`), en la tarjeta del reto, registra la URL pública del paso 4 y
   el token del equipo si la página los pide.
2. Pulsa **VALIDAR ENTRYPOINT** y espera el resultado.
3. Si falla, corre el script del repo contra la misma URL: cada `FALLA` dice qué parte del contrato
   no se cumple, y los logs del paso 5 dicen por qué.

Lo que valida ese botón no está documentado en `material/README.md`: inferido, no verificado, que
prueba el mismo contrato que el script (`/health`, `POST /tasks`, `GET /tasks/{task_id}` con el
token). Si el botón pide algo que el script no cubre, anótalo aquí.

## Qué no hace este runbook

- No despliega solo: el deploy lo hace un integrante con la cuenta del equipo.
- No corre el set oculto del evento.
