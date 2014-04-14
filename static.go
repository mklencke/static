package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// Values may be string or map[string]interface{} or []interface{}
// TODO: should probably be called context
type config map[string]interface{}

const (
	markdownCMD     = "markdown"
	defaultTemplate = "default"
	configFile      = "config.json"
)

var srcDir = flag.String("src", "src", "directory where to find the source files")
var dstDir = flag.String("dst", "dst", "directory to write the output to")

func readConfig(dir string) config {
	fmt.Println("Reading config.")
	f, err := os.Open(filepath.Join(dir, configFile))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	b, err := ioutil.ReadAll(f)
	if err != nil {
		log.Fatal(err)
	}

	c := make(config)
	err = json.Unmarshal(b, &c)
	if err != nil {
		log.Fatal(err)
	}
	return c
}

func checkRequirements() {
	_, err := exec.LookPath(markdownCMD)
	if err != nil {
		log.Fatal(err)
	}
}

func readTemplates(dir string) map[string]*template.Template {
	fmt.Println("Reading templates:")
	paths, err := filepath.Glob(filepath.Join(dir, "*.template"))
	if err != nil {
		log.Fatal(err)
	}

	templates := make(map[string]*template.Template)
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".template")
		fmt.Println("    " + name)
		templates[name], err = template.ParseFiles(path)
		if err != nil {
			log.Fatal(err)
		}
	}
	return templates
}

func clearDir(dir string) {
	fmt.Println("Removing any previous output.")
	paths, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		log.Fatal(err)
	}
	for _, path := range paths {
		err := os.RemoveAll(path)
		if err != nil {
			log.Fatal(err)
		}
	}
}

// Also makes sure everything is a string in there
func cloneConfig(c config) config {
	newc := make(config)
	for k, v := range c {
		switch v := v.(type) {
		case string:
			newc[k] = v
		case map[string]interface{}:
			m := make(map[string]string)
			for k2, v2 := range v {
				m[k2] = v2.(string)
			}
			newc[k] = m
		case []interface{}:
			s := make([]string, 0, len(v))
			for _, v2 := range v {
				s = append(s, v2.(string))
			}
			newc[k] = s
		}
	}
	return newc
}

func convertMarkdown(r io.Reader) []byte {
	cmd := exec.Command("markdown")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal(err)
	}
	var b bytes.Buffer
	cmd.Stdout = &b
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	// TODO handle errors
	io.Copy(stdin, r)
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		log.Fatal(err)
	}
	return b.Bytes()
}

func processPage(name string, src string, dst string, config config, templates map[string]*template.Template) {
	config = cloneConfig(config)
	setRe := regexp.MustCompile("^---set ([a-z]+) (.+)\n?$")
	setBlockRe := regexp.MustCompile("^---setblock ([a-z]+)\n?$")
	setTemplateRe := regexp.MustCompile("^---settemplate ([a-z]+)\n?$")

	templateName := defaultTemplate

	f, err := os.Open(src)
	if err != nil {
		log.Fatal(err)
	}
	var contents bytes.Buffer

	key := ""
	value := ""
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil && err != io.EOF {
			log.Fatal(err)
		}
		matches := setRe.FindSubmatch(line)
		if matches != nil {
			key = string(matches[1])
			value = string(matches[2])
			config[key] = value
			continue
		}
		matches = setBlockRe.FindSubmatch(line)
		if matches != nil {
			key = string(matches[1])
			value = ""
			for {
				line, err := r.ReadBytes('\n')
				if err != nil && err != io.EOF {
					log.Fatal(err)
				}
				if bytes.Equal(line, []byte("---endblock\n")) {
					break
				}
				if err == io.EOF {
					// should never happen
					break
				}
				value += string(line)
			}
			config[key] = value
			continue
		}
		matches = setTemplateRe.FindSubmatch(line)
		if matches != nil {
			templateName = string(matches[1])
			fmt.Println("Setting template: " + templateName)
			continue
		}
		// normal line we should copy
		contents.Write(line)

		// if this is the last line, then stop processing
		if err == io.EOF {
			break
		}
	}
	b := convertMarkdown(&contents)

	t, ok := templates[templateName]
	if !ok {
		log.Fatal("Template " + templateName + " not found.")
	}

	// TODO: faster performance by not casting to string
	config["name"] = name
	config["content"] = string(b)

	var out bytes.Buffer
	err = t.Execute(&out, config)
	if err != nil {
		log.Fatal(err)
	}

	f, err = os.Create(dst)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	io.Copy(f, &out)
}

func processPages(srcdir string, dstdir string, config config, templates map[string]*template.Template) {
	fmt.Println("Processing pages:")
	paths, err := filepath.Glob(filepath.Join(srcdir, "*.page"))
	if err != nil {
		log.Fatal(err)
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".page")
		fmt.Println("    " + name)
		processPage(name, path, filepath.Join(dstdir, name+".html"), config, templates)
	}
}

func copyFile(src string, dst string) {
	if src == dst {
		return
	}

	fin, err := os.Open(src)
	if err != nil {
		log.Fatal(err)
	}
	defer fin.Close()
	fout, err := os.Create(dst)
	if err != nil {
		log.Fatal(err)
	}
	defer fout.Close()
	io.Copy(fout, fin)
}

// TODO: make nested dirs possible
func copyStatics(srcdir string, dstdir string) {
	matches, err := filepath.Glob(filepath.Join(srcdir, "*"))
	if err != nil {
		log.Fatal(err)
	}
	for _, match := range matches {
		if strings.HasSuffix(match, ".page") || strings.HasSuffix(match, ".template") || filepath.Base(match) == "config.json" {
			continue
		}
		copyFile(match, filepath.Join(dstdir, filepath.Base(match)))
	}
}

func main() {
	flag.Parse()
	fmt.Println("Running static...")
	checkRequirements()
	config := readConfig(*srcDir)
	templates := readTemplates(*srcDir)
	clearDir(*dstDir)
	processPages(*srcDir, *dstDir, config, templates)
	copyStatics(*srcDir, *dstDir)
}
