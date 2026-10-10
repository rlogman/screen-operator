package agent

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

// PromptVersion identifica la plantilla de prompts (prompts/v1.tmpl). Se
// registra en la traza de cada tarea.
const PromptVersion = "v1"

//go:embed prompts/v1.tmpl
var promptFS embed.FS

var prompts = template.Must(template.New("v1.tmpl").Option("missingkey=error").ParseFS(promptFS, "prompts/v1.tmpl"))

// render ejecuta una plantilla con nombre.
func render(name string, data any) string {
	var b bytes.Buffer
	if err := prompts.ExecuteTemplate(&b, name, data); err != nil {
		// Las plantillas son fijas y estan cubiertas por tests: un error aqui es
		// un bug de programacion.
		panic(fmt.Sprintf("agent: plantilla %s: %v", name, err))
	}
	return b.String()
}

// SystemPrompt es el prompt de sistema (generico, sin pistas de app).
func SystemPrompt() string { return render("system", nil) }

// stepData es lo que ve el modelo en cada paso, ademas de la imagen.
type stepData struct {
	Instruction  string
	Intent       string
	Entity       string
	Target       string
	Values       string
	Asked        string
	Plan         string
	Today        string
	Phase        Phase
	Preface      bool // clasificar u orientar: todavia no hay fases de accion
	Hints        string
	Notes        []string
	History      []string
	Extraction   string
	Instruction2 string
	StepsLeft    int
	SecondsLeft  int
	ImageDesc    string
}

type extractData struct {
	What    string
	Key     string
	Filters string
}
