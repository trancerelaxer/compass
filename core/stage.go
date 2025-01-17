package core

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/monax/compass/core/docker"
	"github.com/monax/compass/core/helm"
	"github.com/monax/compass/core/kube"
)

// Jobs represent any shell scripts
type Jobs struct {
	Before []string `yaml:"before"`
	After  []string `yaml:"after"`
}

// Stage represents a single part of the deployment pipeline
type Stage struct {
	helm.Chart `yaml:",inline"`
	Abandon    bool     `yaml:"abandon"`   // install only
	Values     string   `yaml:"values"`    // env specific values
	Requires   []string `yaml:"requires"`  // env requirements
	Depends    []string `yaml:"depends"`   // dependencies
	Jobs       Jobs     `yaml:"jobs"`      // bash jobs
	Templates  []string `yaml:"templates"` // templates
}

// Generate renders the given values template
func Generate(name string, data, out *[]byte, values map[string]string) {
	k8s := kube.NewK8s()

	funcMap := template.FuncMap{
		"readEnv":       os.Getenv,
		"getDigest":     docker.GetImageHash,
		"getAuth":       docker.GetAuthToken,
		"fromConfigMap": k8s.FromConfigMap,
		"fromSecret":    k8s.FromSecret,
		"parseJSON":     kube.ParseJSON,
	}

	t, err := template.New(name).Funcs(funcMap).Parse(string(*data))
	if err != nil {
		log.Fatalf("failed to render %s : %s\n", name, err)
	}

	buf := new(bytes.Buffer)
	err = t.Execute(buf, values)
	if err != nil {
		log.Fatalf("failed to render %s : %s\n", name, err)
	}
	*out = append(*out, buf.Bytes()...)
}

// Extrapolate renders a template and reads it to a map
func Extrapolate(tpl string, values map[string]string) map[string]string {
	if tpl == "" {
		return values
	}
	data, err := ioutil.ReadFile(tpl)
	if err != nil {
		log.Fatalf("couldn't read from %s\n", tpl)
	}
	var out []byte
	Generate(tpl, &data, &out, values)
	MergeVals(values, LoadVals(tpl, out))
	return values
}

func shellVars(vals map[string]string) []string {
	envs := make([]string, len(vals))
	for key, value := range vals {
		envs = append(envs, fmt.Sprintf("%s=%s", key, value))
	}
	return envs
}

func shellJobs(values []string, jobs []string, verbose bool) error {
	for _, command := range jobs {
		log.Printf("running job: %s\n", command)
		args := strings.Fields(command)
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(values, os.Environ()...)
		stdout, err := cmd.Output()
		if verbose && stdout != nil {
			fmt.Println(string(stdout))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func checkRequires(values map[string]string, reqs []string) error {
	for _, r := range reqs {
		if _, exists := values[r]; !exists {
			return errors.New("requirement not met")
		}
	}
	return nil
}

func cpVals(prev map[string]string) map[string]string {
	// copy values from main for individual chart
	values := make(map[string]string, len(prev))
	for k, v := range prev {
		values[k] = v
	}
	return values
}

// Destroy deletes the chart once its dependencies have been met
func (stage *Stage) Destroy(conn *helm.Bridge, key string, values map[string]string, verbose bool, deps *Depends) error {
	defer deps.Complete(stage.Depends...)

	err := checkRequires(values, stage.Requires)
	if err != nil {
		return err
	}

	deps.Wait(key)
	log.Printf("deleting %s\n", stage.Release)
	return conn.DeleteRelease(stage.Release)
}

// Create deploys the chart once its dependencies have been met
func (stage *Stage) Create(conn *helm.Bridge, key string, main map[string]string, verbose bool, deps *Depends) error {
	defer deps.Complete(key)

	_, err := conn.ReleaseStatus(stage.Release)
	if err == nil && stage.Abandon {
		return errors.New("chart already installed")
	}

	values := cpVals(main)
	MergeVals(values, LoadVals(stage.Values, nil))
	MergeVals(values, map[string]string{"namespace": stage.Namespace})
	MergeVals(values, map[string]string{"release": stage.Release})

	err = checkRequires(values, stage.Requires)
	if err != nil {
		return err
	}

	deps.Wait(stage.Depends...)

	shellJobs(shellVars(values), stage.Jobs.Before, verbose)
	defer shellJobs(shellVars(values), stage.Jobs.After, verbose)

	var out []byte
	for _, temp := range stage.Templates {
		data, read := ioutil.ReadFile(temp)
		if read != nil {
			panic(read)
		}
		Generate(temp, &data, &out, values)
	}

	if verbose {
		fmt.Println(string(out))
	}

	status, err := conn.ReleaseStatus(stage.Release)
	if status == "PENDING_INSTALL" || err != nil {
		if err == nil {
			log.Printf("deleting release: %s\n", stage.Release)
			conn.DeleteRelease(stage.Release)
		}
		log.Printf("installing release: %s\n", stage.Release)
		err := conn.InstallChart(stage.Chart, out)
		if err != nil {
			log.Fatalf("failed to install %s : %s\n", stage.Release, err)
		}
		log.Printf("release %s installed\n", stage.Release)
		return nil
	}

	log.Printf("upgrading release: %s\n", stage.Release)
	conn.UpgradeChart(stage.Chart, out)
	if err != nil {
		log.Fatalf("failed to install %s : %s\n", stage.Release, err)
	}
	log.Printf("release upgraded: %s\n", stage.Release)
	return nil
}
