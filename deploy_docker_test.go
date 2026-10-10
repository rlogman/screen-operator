//go:build docker

package screenoperator_test

// Tests de la historia 4.1 contra la imagen real. Necesitan el daemon de
// Docker; sin el, se saltan con un mensaje claro (ENVIRONMENTAL).
//
//	go test -tags docker -run '^TestE4_1_' -count=1 .

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const imageTag = "screen-operator"

var (
	buildOnce sync.Once
	buildOut  string
	buildErr  error
)

func requireDocker(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("ENVIRONMENTAL: el daemon de Docker no responde (%v): %s", err, bytes.TrimSpace(out))
	}
}

// dockerBuild ejecuta 'docker build -t screen-operator .' una vez por corrida.
func dockerBuild(t *testing.T) {
	t.Helper()
	requireDocker(t)
	root := repoRoot(t)
	buildOnce.Do(func() {
		cmd := exec.Command("docker", "build", "-t", imageTag, ".")
		cmd.Dir = root
		b, err := cmd.CombinedOutput()
		buildOut, buildErr = string(b), err
	})
	if buildErr != nil {
		t.Fatalf("docker build -t %s .: %v\n%s", imageTag, buildErr, buildOut)
	}
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	b, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestE4_1_S1_DockerBuildSinToolchain(t *testing.T) {
	dockerBuild(t)
	id := docker(t, "create", imageTag)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })

	cmd := exec.Command("docker", "export", id)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(stdout)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, strings.TrimPrefix(h.Name, "./"))
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	var agent, config bool
	for _, n := range names {
		base := n[strings.LastIndex(n, "/")+1:]
		switch {
		case strings.HasPrefix(n, "usr/local/go/"), strings.HasPrefix(n, "go/pkg/"),
			base == "go" || base == "gofmt":
			t.Errorf("la etapa final contiene toolchain de Go: %s", n)
		case base == ".env" || strings.HasPrefix(n, ".git/") || strings.Contains(n, "/.git/") || strings.Contains(n, ".artifex/"):
			t.Errorf("la imagen contiene un archivo que no debe entrar: %s", n)
		case n == "app/agent":
			agent = true
		case n == "app/config/base.yaml":
			config = true
		}
	}
	if !agent || !config {
		t.Fatalf("la imagen no trae /app/agent (%v) o /app/config/base.yaml (%v)", agent, config)
	}
}

func TestE4_1_S2_DockerRunHealth200(t *testing.T) {
	dockerBuild(t)
	id := docker(t, "run", "-d", "-e", "TEAM_TOKEN=t0k", "-e", "PORT=8080", "-p", "127.0.0.1::8080", imageTag)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	hostPort := docker(t, "port", id, "8080/tcp")
	hostPort = strings.Split(hostPort, "\n")[0]

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var last string
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostPort+"/health", nil)
		req.Header.Set("Authorization", "Bearer t0k")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = resp.Status + " " + string(body)
		} else {
			last = err.Error()
		}
		time.Sleep(300 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", id).CombinedOutput()
	t.Fatalf("GET /health no respondio 200 (ultimo: %s)\nlogs:\n%s", last, logs)
}

func TestE4_1_S3_DockerRunSinTeamTokenFalla(t *testing.T) {
	dockerBuild(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	name := "so-e4-s3-" + time.Now().Format("150405.000000")
	name = strings.ReplaceAll(name, ".", "")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	cmd := exec.CommandContext(ctx, "docker", "run", "--name", name, "-e", "PORT=8080", imageTag)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("el contenedor sin TEAM_TOKEN siguio corriendo:\n%s", out)
	}
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() == 0 {
		t.Fatalf("el contenedor sin TEAM_TOKEN termino con %v, se esperaba codigo != 0:\n%s", err, out)
	}
	if !strings.Contains(string(out), "TEAM_TOKEN") {
		t.Fatalf("el mensaje no nombra TEAM_TOKEN:\n%s", out)
	}
}

func TestE4_2_S3_CheckEntrypointContraContenedor(t *testing.T) {
	dockerBuild(t)
	id := docker(t, "run", "-d", "-e", "TEAM_TOKEN=tok-docker-e4", "-e", "PORT=8080", "-p", "127.0.0.1::8080", imageTag)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	base := "http://" + strings.Split(docker(t, "port", id, "8080/tcp"), "\n")[0]
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", id).CombinedOutput()
			t.Fatalf("el contenedor no respondio /health:\n%s", logs)
		}
		time.Sleep(200 * time.Millisecond)
	}
	code, out := runCheckScript(t, nil, base, "tok-docker-e4")
	if code != 0 || !strings.Contains(out, "RESULTADO: entrypoint OK") {
		t.Fatalf("check_entrypoint.sh contra el contenedor: codigo %d:\n%s", code, out)
	}
	t.Log(out)
}
