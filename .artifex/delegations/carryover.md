# Arrastre de observaciones entre épicas

Cada brief de épica que toque estos puntos debe resolverlos y testearlos.

## Para el adaptador Driver RFB → tasks (épica 6, o donde se conecte el driver real)
- should-fix (validador so-epic-3): `internal/tasks/manager.go:231` llama `Dialer.Dial(ctx, ...)` síncrono; el adaptador debe respetar ctx (cancelar dial al vencer el deadline) y tener test.
- should-fix: `internal/tasks/gate.go:35-49` sostiene el mutex durante `sess.Send`; el adaptador debe poner write deadline / respetar ctx en Send y Close para que el reporte no se cuelgue. Test con un driver que bloquea.
- should-fix: el `recover` solo cubre el Runner (`manager.go:242-249`); agregar recover en `work()` para Dial y Close.
- nit: el redactor de tarea no conoce la URL cruda; errores del driver no deben incluir `user:pass@` ni query (`manager.go:237`).
