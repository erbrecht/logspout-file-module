package file

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/gliderlabs/logspout/router"
)

//
// file route exaple:
//   file://sample.log?maxfilesize=102400
//

func init() {
	router.AdapterFactories.Register(NewFileAdapter, "file")
}

var funcs = template.FuncMap{
	"toJSON": func(value interface{}) string {
		bytes, err := json.Marshal(value)
		if err != nil {
			log.Println("error marshalling to JSON: ", err)
			return "null"
		}
		return string(bytes)
	},
}

// NewFileAdapter returns a configured raw.Adapter
func NewFileAdapter(route *router.Route) (router.LogAdapter, error) {
	// default action is to NOT check that log file exists before writing log entry
	// if the file is removed while logspout is running, entries will be lost since
	// logspout will have a file pointer to a non-existent file

	// setting CHECK_LOG_FILE environment variable to any value will force check that
	// the log file exists before writing a log entry. this could induce a large
	// performance penalty since the file is checked (stat'ed) before every write

	checkLogFileExists := false
	os.Environ()
	if os.Getenv("CHECK_LOG_FILE") != "" {
		checkLogFileExists = true
	}

	// default log dir
	logdir := "/var/log/"

	// get 'filename' from route.Address
	filename := "default.log"
	if route.Address != "" {
		filename = route.Address
	}
	//log.Println("filename [",filename,"]")

	structuredData := route.Options["structured_data"]

	tmplStr := "{ \"container\" : \"{{ .Container.Name }}\", \"labels\": {{ toJSON .Container.Config.Labels }}, \"timestamp\": \"{{ .Time.Format \"2006-01-02T15:04:05Z0700\" }}\", \"source\" : \"{{ .Source }}\", \"line\": {{.Data}}\n"
	if structuredData != "true" {
		tmplStr = "{ \"container\" : \"{{ .Container.Name }}\", \"labels\": {{ toJSON .Container.Config.Labels }}, \"timestamp\": \"{{ .Time.Format \"2006-01-02T15:04:05Z0700\" }}\", \"source\" : \"{{ .Source }}\", \"line\": {{ toJSON .Data }} }\n"
	}
	tmpl, err := template.New("file").Funcs(funcs).Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	// default maxfilecount 10
	maxfilecount := 10
	if route.Options["maxfilecount"] != "" {
		maxcountStr := route.Options["maxfilecount"]
		maxcount, err := strconv.Atoi(maxcountStr)
		if err == nil {
			maxfilecount = maxcount
		}
	}

	// default max size (100Mb)
	maxfilesize := 1024 * 1024 * 100
	if route.Options["maxfilesize"] != "" {
		szStr := route.Options["maxfilesize"]
		sz, err := strconv.Atoi(szStr)
		if err == nil {
			maxfilesize = sz
		}
	}
	//log.Println("maxfilesize [",maxfilesize,"]")

	a := Adapter{
		route:        route,
		filename:     filename,
		logdir:       logdir,
		maxfilesize:  maxfilesize,
		maxfilecount: maxfilecount,
		tmpl:         tmpl,
		checklogfile: checkLogFileExists,
	}

	// rename if exists, otherwise create it
	err = a.Rotate()
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// Adapter is a simple adapter that streams log output to a connection without any templating
type Adapter struct {
	filename     string
	logdir       string
	filesize     int
	maxfilesize  int
	maxfilecount int
	fp           *os.File
	route        *router.Route
	tmpl         *template.Template
	checklogfile bool
}

// CheckFile makes sure file exists for writing
func (a *Adapter) CheckFile() (err error) {
	if _, err := os.Stat(a.logdir + a.filename); os.IsNotExist(err) {
		// file doesn't exist. create it
		a.fp, err = os.Create(a.logdir + a.filename)
		// set size to 0
		if err != nil {
			return err
		}
		a.filesize = 0
	}
	return nil
}

// Stream sends log data to a connection
func (a *Adapter) Stream(logstream chan *router.Message) {
	for message := range logstream {
		buf := new(bytes.Buffer)
		err := a.tmpl.Execute(buf, message)
		if err != nil {
			log.Println("err:", err)
			return
		}

		if a.checklogfile {
			a.CheckFile()
		}

		//log.Println("debug:", buf.String())
		_, err = a.fp.Write(buf.Bytes())
		if err != nil {
			log.Println("err:", err)
		}

		// update file size
		a.filesize = a.filesize + len(buf.Bytes())

		// rotate file if size exceed max size
		if a.filesize > a.maxfilesize {
			a.Rotate()
		}
	}
}

// PruneLogs removes old log files
func (a *Adapter) PruneLogs() (err error) {
	// get listing of directory entries
	entries, err := ioutil.ReadDir(a.logdir)
	if err != nil {
		return err
	}

	// limit to regular files that contain the appropriate file name
	files := []os.FileInfo{}
	for _, entry := range entries {
		if entry.Mode().IsRegular() && strings.Contains(entry.Name(), a.filename) {
			files = append(files, entry)
		}
	}

	// sort files by modified date
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })

	// grab all but last <maxfilecount> files
	toPrune := files[0 : len(files)-a.maxfilecount]

	// remove files
	for _, fi := range toPrune {
		os.Remove(a.logdir + fi.Name())
	}

	return nil
}

// Rotate rotates log file
func (a *Adapter) Rotate() (err error) {
	// Close existing file if open
	if a.fp != nil {
		err = a.fp.Close()
		//log.Println("Close existing file pointer")
		a.fp = nil
		if err != nil {
			return err
		}
	}
	// Rename dest file if it already exists
	_, err = os.Stat(a.logdir + a.filename)
	if err == nil {
		err = os.Rename(a.logdir+a.filename, a.logdir+a.filename+"."+time.Now().Format(time.RFC3339))
		log.Println("Rename existing log file")
		if err != nil {
			return err
		}
	}
	// Create new file.
	a.fp, err = os.Create(a.logdir + a.filename)
	log.Println("Create new log file")
	if err != nil {
		return err
	}
	a.filesize = 0
	return nil
}
