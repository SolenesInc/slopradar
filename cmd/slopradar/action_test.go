package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestActionSourcePathIsCanonical(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repository, "scripts", "action-path.sh")
	dottedPath := repository + string(os.PathSeparator) + ".//"
	command := exec.Command("bash", script, dottedPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve action path: %v\n%s", err, output)
	}
	if resolved := strings.TrimSpace(string(output)); resolved != repository {
		t.Fatalf("resolved path = %q, want %q", resolved, repository)
	}
}

func TestActionReportFetchesBaseAndWritesMarkdown(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	repository := filepath.Join(root, "repository")
	checkout := filepath.Join(root, "checkout")
	gitCommand(t, root, "init", "--bare", remote)
	gitCommand(t, root, "init", "-b", "main", repository)
	gitCommand(t, repository, "config", "user.name", "Slopradar Test")
	gitCommand(t, repository, "config", "user.email", "test@slopradar.invalid")
	writeFile(t, repository, "source.go", "package fixture\n")
	gitCommand(t, repository, "add", ".")
	gitCommand(t, repository, "commit", "-m", "base")
	gitCommand(t, repository, "remote", "add", "origin", remote)
	gitCommand(t, repository, "push", "-u", "origin", "main")
	gitCommand(t, repository, "checkout", "-b", "feature")
	writeFile(t, repository, "source.go", cc12Source())
	gitCommand(t, repository, "add", ".")
	gitCommand(t, repository, "commit", "-m", "add complex function")
	gitCommand(t, repository, "push", "origin", "feature")
	gitCommand(t, repository, "checkout", "main")
	gitCommand(t, repository, "merge", "--no-ff", "feature", "-m", "pull request merge")
	gitCommand(t, repository, "push", "origin", "HEAD:refs/pull/1/merge")

	gitCommand(t, root, "init", checkout)
	gitCommand(t, checkout, "remote", "add", "origin", "file://"+remote)
	gitCommand(t, checkout, "fetch", "--depth=1", "origin", "refs/pull/1/merge")
	gitCommand(t, checkout, "checkout", "--detach", "FETCH_HEAD")
	if shallow := strings.TrimSpace(gitCommand(t, checkout, "rev-parse", "--is-shallow-repository")); shallow != "true" {
		t.Fatalf("test checkout is shallow: %s", shallow)
	}

	binary := filepath.Join(root, "bin", "slopradar")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build slopradar: %v\n%s", err, output)
	}

	reportPath := filepath.Join(root, "report.md")
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "action-report.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", scriptPath, "main", "0", reportPath)
	command.Dir = checkout
	command.Env = append(os.Environ(), "PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("action report: %v\n%s", err, output)
	}

	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(report), "<!-- slopradar -->\n") || !strings.Contains(string(report), "complex") || !strings.Contains(string(report), "CC 12") {
		t.Fatalf("report = %s", report)
	}
}

func TestActionReportTreatsBaseAsData(t *testing.T) {
	root := t.TempDir()
	touched := filepath.Join(root, "injected")
	reportPath := filepath.Join(root, "report.md")
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "action-report.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", scriptPath, "main;touch "+touched, "0", reportPath)
	command.Dir = root
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "base input is not a valid branch name") {
		t.Fatalf("error = %v, output = %q", err, output)
	}
	if _, err := os.Stat(touched); !os.IsNotExist(err) {
		t.Fatalf("injected path exists: %v", err)
	}
}
