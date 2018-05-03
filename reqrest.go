package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/urfave/cli"
)

const queryTemplate = `{
    "query": {
        
    }
}
`

const jsonTemplate = `{ 
    
}
`

const (
	CONTENT_TYPE_JSON    = "application/json"
	CONTENT_QUERY_EDITOR = "vim +3 +'normal $' +startinsert"
	CONTENT_EDITOR       = "vim +2 +'normal $' +startinsert"
)

const (
	METHOD_GET = iota
	METHOD_PUT
	METHOD_DELETE
	METHOD_POST
)

func getContent(inputfile, templete, contentEditor string) (string, error) {
	var stderr bytes.Buffer

	tmpfile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", err
	}
	_, err = io.WriteString(tmpfile, templete)
	if err != nil {
		return "", err
	}
	err = tmpfile.Close()
	if err != nil {
		return "", err
	}
	filename := tmpfile.Name() + ".json"
	os.Rename(tmpfile.Name(), filename)

	if contentEditor == "" {
		return filename, nil
	}

	cmd := exec.Cmd{
		Path:   "/bin/sh",
		Args:   []string{"sh", "-c", contentEditor + " " + filename},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: &stderr,
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	err = cmd.Wait()
	if err != nil {
		return "", fmt.Errorf("error run Editor(%s), stderr: %s", contentEditor, stderr.String())
	}

	return filename, nil
}

func doRestRequest(url, method, contentType, inputfile, contentEditor string) (string, map[string][]string, []byte, error) {
	var err error
	filename := inputfile
	if len(filename) == 0 {
		templete := jsonTemplate
		if contentEditor == CONTENT_QUERY_EDITOR {
			templete = queryTemplate
		}
		filename, err = getContent(inputfile, templete, contentEditor)
		if err != nil {
			return "", nil, nil, err
		}
		defer os.Remove(filename)
	}

	c, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", nil, nil, err
	}

	compact := new(bytes.Buffer)
	indent := new(bytes.Buffer)
	if len(c) != 0 {
		err = json.Compact(compact, c)
		if err != nil {
			return "", nil, nil, err
		}
		err = json.Indent(indent, c, "", "    ")
		if err != nil {
			return "", nil, nil, err
		}
	}

	content := compact
	req, err := http.NewRequest(method, url, content)
	if err != nil {
		return "", nil, nil, err
	}
	if contentType != "" && content.Len() != 0 {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, nil, err
	}
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", nil, nil, err
	}

	return resp.Status + " " + resp.Proto, map[string][]string(resp.Header), respBody, nil
}

func printResponse(status string, headers map[string][]string, respBody []byte, pretty bool) error {
	if headers != nil {
		fmt.Println(status)
		for k, vs := range headers {
			for _, v := range vs {
				fmt.Printf("%s: %v\n", k, v)
			}
		}
		fmt.Println("")
	}

	if pretty {
		indentBody := bytes.NewBuffer(make([]byte, 64))
		err := json.Indent(indentBody, respBody, "", "    ")
		if err != nil {
			return err
		}
		fmt.Println(indentBody.String())
		return nil
	}
	fmt.Println(string(respBody))
	return nil
}

func doSubCmd(c *cli.Context, method, editor string) error {
	url := c.Args().Get(0)
	if strings.Index(url, "http://") != 0 {
		url = "http://" + url
	}
	status, headers, content, err := doRestRequest(url, method, CONTENT_TYPE_JSON, c.GlobalString("file"), editor)
	if err != nil {
		return err
	}
	if c.GlobalBool("slient") {
		return nil
	}
	if !c.GlobalBool("headers") {
		headers = nil
		status = ""
	}
	return printResponse(status, headers, content, c.GlobalBool("pretty"))
}

func main() {
	app := cli.NewApp()
	app.Name = "reqrest"
	app.Usage = "Send request with RESTful API"
	app.Version = "1.0"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "editor, e",
			Usage:  "Use `EDITOR` to edit input content",
			EnvVar: "REQREST_EDITOR",
		},
		cli.StringFlag{
			Name:  "file, f",
			Usage: "Input the `FILE` content",
		},
		cli.BoolFlag{
			Name:  "pretty, p",
			Usage: "Indent the response json content",
		},
		cli.BoolFlag{
			Name:  "slient, s",
			Usage: "Don't output the response",
		},
		cli.BoolFlag{
			Name:  "headers, H",
			Usage: "Output the http headers",
		},
	}

	app.Commands = []cli.Command{
		{
			Name:  "get",
			Usage: "request with GET method",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "query, q",
					Usage: "Send query request",
				},
			},
			Action: func(c *cli.Context) error {
				editor := c.GlobalString("editor")
				if c.Bool("query") && editor == "" {
					editor = CONTENT_QUERY_EDITOR
				}
				return doSubCmd(c, http.MethodGet, editor)
			},
		},
		{
			Name:  "post",
			Usage: "request with POST method",
			Action: func(c *cli.Context) error {
				editor := CONTENT_EDITOR
				if len(c.GlobalString("editor")) != 0 {
					editor = c.GlobalString("editor")
				}
				return doSubCmd(c, http.MethodPost, editor)
			},
		},
		{
			Name:  "put",
			Usage: "request with PUT method",
			Action: func(c *cli.Context) error {
				editor := CONTENT_EDITOR
				if len(c.GlobalString("editor")) != 0 {
					editor = c.GlobalString("editor")
				}
				return doSubCmd(c, http.MethodPut, editor)
			},
		},
		{
			Name:  "delete",
			Usage: "request with DELETE method",
			Action: func(c *cli.Context) error {
				return doSubCmd(c, http.MethodDelete, c.GlobalString("editor"))
			},
		},
	}

	app.Before = func(c *cli.Context) error {
		file := c.GlobalString("file")
		if len(file) != 0 {
			_, err := os.Stat(file)
			if err != nil {
				return err
			}
		}
		return nil
	}

	app.Action = func(c *cli.Context) error {
		args := make([]string, 1+len(os.Args))
		args[0] = os.Args[0]
		args[1] = "get"
		copy(args[2:], os.Args[1:])

		return app.Run(args)
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
