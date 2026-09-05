package framework

import (
	"regexp"
	"strings"
)

// DefaultGoVersion builds Go projects that do not pin one.
const DefaultGoVersion = "1.24"

var goDirectiveRe = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

// detectGo identifies a Go module and where its main package lives.
func detectGo(src Source) *Result {
	raw, err := src.Read("go.mod")
	if err != nil {
		return nil
	}

	version := DefaultGoVersion
	if m := goDirectiveRe.FindSubmatch(raw); len(m) == 2 {
		version = string(m[1])
	}

	r := &Result{
		ID: "go", Name: "Go", Mode: ModeServer, Port: 8080,
		Builder: "golang:" + version + "-alpine",
		Runtime: "alpine:3.21",
		Start:   "/app/server",
	}

	// Where the main package lives decides the build target, and getting
	// it wrong is the difference between a binary and a compile error.
	switch {
	case src.Exists("main.go"):
		r.Build = "go build -trimpath -ldflags='-s -w' -o /out/server ."
	case src.Exists("cmd/server/main.go"):
		r.Build = "go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/server"
	case src.Exists("cmd/api/main.go"):
		r.Build = "go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/api"
	case src.Exists("cmd/app/main.go"):
		r.Build = "go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/app"
	default:
		r.Build = "go build -trimpath -ldflags='-s -w' -o /out/server ."
		r.Notes = append(r.Notes,
			"publix could not find the main package. It will build the module root; if your entry point is under cmd/, set build.command in deployment.yaml.")
	}
	return r
}

// detectPython identifies a Python web application and how to serve it.
func detectPython(src Source) *Result {
	hasRequirements := src.Exists("requirements.txt")
	hasPyproject := src.Exists("pyproject.toml")
	if !hasRequirements && !hasPyproject && !src.Exists("manage.py") {
		return nil
	}

	r := &Result{
		ID: "python", Name: "Python", Mode: ModeServer, Port: 8000,
		// The builder is the full image so a dependency with a C
		// extension compiles without installing a toolchain at build
		// time; only the slim runtime is ever shipped.
		Builder: "python:3.12",
		Runtime: "python:3.12-slim",
	}

	switch {
	case hasRequirements:
		r.Install = "pip install --no-cache-dir -r requirements.txt"
	case src.Exists("poetry.lock"):
		r.Install = "pip install --no-cache-dir poetry && poetry config virtualenvs.create false && poetry install --no-interaction --no-root"
		r.Name = "Python (Poetry)"
	case src.Exists("uv.lock"):
		r.Install = "pip install --no-cache-dir uv && uv sync --frozen --no-dev"
		r.Name = "Python (uv)"
	default:
		r.Install = "pip install --no-cache-dir ."
	}

	deps := strings.ToLower(readText(src, "requirements.txt") + readText(src, "pyproject.toml"))

	switch {
	case src.Exists("manage.py"):
		r.ID, r.Name = "django", "Django"
		// Django's WSGI module is named after the project package, which
		// is a directory publix cannot see without listing subdirectories.
		r.Start = "gunicorn --bind 0.0.0.0:8000 ${DJANGO_WSGI_MODULE:-config.wsgi}:application"
		r.Notes = append(r.Notes,
			"Django needs its WSGI module name. publix assumes `config.wsgi`; set DJANGO_WSGI_MODULE in the project's environment, or set build.start in deployment.yaml.")
		if !strings.Contains(deps, "gunicorn") {
			r.Install += " && pip install --no-cache-dir gunicorn"
		}

	case strings.Contains(deps, "fastapi"):
		r.ID, r.Name = "fastapi", "FastAPI"
		r.Start = "uvicorn " + pythonModule(src, "main") + ":app --host 0.0.0.0 --port 8000"
		if !strings.Contains(deps, "uvicorn") {
			r.Install += " && pip install --no-cache-dir 'uvicorn[standard]'"
		}

	case strings.Contains(deps, "flask"):
		r.ID, r.Name = "flask", "Flask"
		r.Start = "gunicorn --bind 0.0.0.0:8000 " + pythonModule(src, "app") + ":app"
		if !strings.Contains(deps, "gunicorn") {
			r.Install += " && pip install --no-cache-dir gunicorn"
		}

	default:
		r.Mode = ModeUnknown
		r.Notes = append(r.Notes,
			"This is a Python project, but publix could not tell how to serve it. Set build.start in deployment.yaml, for example `uvicorn app:app --host 0.0.0.0 --port 8000`.")
	}
	return r
}

// pythonModule picks the module a web app is likely defined in.
func pythonModule(src Source, preferred string) string {
	for _, candidate := range []string{preferred, "main", "app", "server", "wsgi", "asgi"} {
		if src.Exists(candidate + ".py") {
			return candidate
		}
		if src.Exists("app/" + candidate + ".py") {
			return "app." + candidate
		}
		if src.Exists("src/" + candidate + ".py") {
			return "src." + candidate
		}
	}
	return preferred
}
