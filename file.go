package file

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strconv"
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
	// default log dir
	logdir := "/var/log/"

	// get 'filename' from route.Address
	filename := "default.log"
	if route.Address != "" {
		filename = route.Address
	}
	//log.Println("filename [",filename,"]")

	//tmplStr := "{ \"container\" : \"{{ .Container.Name }}\", \"labels\": {{ toJSON .Container.Config.Labels }}, \"timestamp\": \"{{ .Time.Format \"2006-01-02T15:04:05Z0700\" }}\", \"source\" : \"{{ .Source }}\", \"line\": {{.Data}}\n"
	//if structuredData != "true" {
	tmplStr := "{ \"container\" : \"{{ .Container.Name }}\", \"labels\": {{ toJSON .Container.Config.Labels }}, \"timestamp\": \"{{ .Time.Format \"2006-01-02T15:04:05Z0700\" }}\", \"source\" : \"{{ .Source }}\", \"line\": {{ toJSON .Data }} }\n"
	//}
	tmpl, err := template.New("file").Funcs(funcs).Parse(tmplStr)
	if err != nil {
		return nil, err
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
		route:       route,
		filename:    filename,
		logdir:      logdir,
		maxfilesize: maxfilesize,
		tmpl:        tmpl,
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
	filename    string
	logdir      string
	filesize    int
	maxfilesize int
	fp          *os.File
	route       *router.Route
	tmpl        *template.Template
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

		a.CheckFile()

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

// Rotate log file
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
