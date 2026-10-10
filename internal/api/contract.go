package api

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"screenoperator/internal/config"
	"screenoperator/internal/tasks"
)

// Route es un metodo y una ruta del contrato.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Contract son los nombres del contrato del evento (contract.* en la configuracion).
type Contract struct {
	TokenEnv            string
	Health, Create, Get Route
	TaskField           string
	DesktopURLField     string
	DesktopSecretFields []string
	IDField             string
	StatusField         string
	SummaryField        string
	StatusValues        map[tasks.Status]string
	CreateTimeout       time.Duration
}

type rawContract struct {
	Auth struct {
		Scheme   string `json:"scheme"`
		TokenEnv string `json:"token_env"`
	} `json:"auth"`
	Routes struct {
		Health Route `json:"health"`
		Create Route `json:"create"`
		Get    Route `json:"get"`
	} `json:"routes"`
	Request struct {
		TaskField           string   `json:"task_field"`
		DesktopURLField     string   `json:"desktop_url_field"`
		DesktopSecretFields []string `json:"desktop_secret_fields"`
	} `json:"request"`
	Response struct {
		IDField      string `json:"id_field"`
		StatusField  string `json:"status_field"`
		SummaryField string `json:"summary_field"`
		StatusValues struct {
			Running string `json:"running"`
			Done    string `json:"done"`
			Failed  string `json:"failed"`
		} `json:"status_values"`
	} `json:"response"`
	CreateTimeoutMS int `json:"create_timeout_ms"`
}

var reGetParam = regexp.MustCompile(`^/[^{}]*\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// ContractFromConfig lee contract.* de una configuracion ya validada.
func ContractFromConfig(c *config.Config) (Contract, error) {
	var r rawContract
	if err := c.Decode("contract", &r); err != nil {
		return Contract{}, err
	}
	if !strings.EqualFold(r.Auth.Scheme, "bearer") {
		return Contract{}, fmt.Errorf("contract.auth.scheme %q no soportado", r.Auth.Scheme)
	}
	if !reGetParam.MatchString(r.Routes.Get.Path) {
		return Contract{}, fmt.Errorf("contract.routes.get.path %q debe terminar en un parametro {nombre}", r.Routes.Get.Path)
	}
	return Contract{
		TokenEnv:            r.Auth.TokenEnv,
		Health:              r.Routes.Health,
		Create:              r.Routes.Create,
		Get:                 r.Routes.Get,
		TaskField:           r.Request.TaskField,
		DesktopURLField:     r.Request.DesktopURLField,
		DesktopSecretFields: r.Request.DesktopSecretFields,
		IDField:             r.Response.IDField,
		StatusField:         r.Response.StatusField,
		SummaryField:        r.Response.SummaryField,
		StatusValues: map[tasks.Status]string{
			tasks.StatusRunning: r.Response.StatusValues.Running,
			tasks.StatusDone:    r.Response.StatusValues.Done,
			tasks.StatusFailed:  r.Response.StatusValues.Failed,
		},
		CreateTimeout: time.Duration(r.CreateTimeoutMS) * time.Millisecond,
	}, nil
}

// getParam es el nombre del parametro de GET /tasks/{task_id}.
func (c Contract) getParam() string {
	m := reGetParam.FindStringSubmatch(c.Get.Path)
	if m == nil {
		return ""
	}
	return m[1]
}
