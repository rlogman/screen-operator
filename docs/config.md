# Configuracion declarativa

## Principio

El agente no trae conocimiento de la app: la evaluacion corre sobre una app que nunca vio
(material/README.md, "Como se evalua"). La configuracion separa lo que SI podemos anticipar que
cambia en cuatro capas, y deja la app como hints opcionales.

| Capa | Archivo | Que contiene | Quien la escribe |
|---|---|---|---|
| Base | `config/base.yaml` | Contrato HTTP, formato del resumen, presupuestos, modelos, politicas de seguridad | Nosotros |
| Superficie | `config/surfaces/<id>.yaml` | Transporte, pantalla, temporizacion de input, deteccion de pantalla estable | Nosotros |
| App | `config/apps/<id>.yaml` | Entidades, identificadores, navegacion, quirks. Opcional | Nosotros o el agente (mismo formato) |
| Overlay | `SO_CONFIG_OVERLAY` (ruta a YAML) | Ajustes de despliegue sin tocar el repo | Operador |

Merge por tarea: `base <- superficie <- app <- overlay`. Mapas en profundidad; listas y escalares
reemplazan. El resultado se valida contra `config/schema.json` al arrancar y en cada recarga;
una clave desconocida es error (atrapa typos como `clik_hold_ms`).

Seleccion en runtime:
- Superficie: por la URL del escritorio que llega en `POST /tasks` (`surface.match`).
- App: por texto visible en la primera captura (`app.match.screen_text_any`). Sin match, perfil vacio.

## Que se edita segun el cambio

| Si cambia... | Se edita | Requiere codigo |
|---|---|---|
| Nombres de campos o rutas del contrato, valores de `estado` | `contract.*` | No |
| Formato de la linea `respuesta:` o separador | `summary.*` | No |
| Tiempo por tarea | `budgets.task_deadline_s` | No |
| Resolucion de pantalla | `surface.screen` (o se toma de ServerInit con `trust_server_init`) | No |
| Repintado mas lento, clics ignorados, concatenacion al escribir | `surface.settle`, `surface.input` | No |
| Atajo para seleccionar todo (p. ej. app nativa) | `surface.input.replace_text` | No |
| Modelo, costo o calidad de captura | `models.roles`, `perception` | No |
| Que se considera peligroso o ambiguo | `policy.*` | No |
| Dominio de la app (otra app conocida de antemano) | nuevo `config/apps/<id>.yaml` | No |
| Protocolo distinto de RFB (RDP, X11, otro) | `surface.transport` + driver nuevo detras de la interfaz `Driver` | Si |
| Contrato con semantica nueva (p. ej. streaming, callbacks) | `contract` + handler | Si |

## Limites honestos

- Un perfil de app escrito a mano no ayuda en la evaluacion (app no vista). Su valor: medir sobre
  SIGECOM y fijar el formato que el agente usa para guardar lo que aprende durante una tarea.
- `surface.transport` solo admite drivers implementados. El esquema lo restringe con `enum` para
  que una configuracion no prometa algo que el codigo no hace.
- `models.roles.*.model` arranca en `openai/gpt-4o-mini` porque es el modelo que la pagina de
  herramientas del evento usa en su verificacion. La eleccion final sale de medir con el harness.

## Validar

```bash
python3 scripts/validate_config.py config/schema.json config/base.yaml config/surfaces/vnc-kiosk.yaml config/apps/sigecom.yaml
```
