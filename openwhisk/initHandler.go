/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package openwhisk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

type initBodyRequest struct {
	Code   string                 `json:"code,omitempty"`
	Binary bool                   `json:"binary,omitempty"`
	Main   string                 `json:"main,omitempty"`
	Env    map[string]interface{} `json:"env,omitempty"`
}

type initRequest struct {
	Value initBodyRequest `json:"value,omitempty"`
}

func sendOK(w http.ResponseWriter) {
	// answer OK
	w.Header().Set("Content-Type", "application/json")
	buf := []byte("{\"ok\":true}\n")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(buf)))
	w.Write(buf)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (ap *ActionProxy) initHandler(w http.ResponseWriter, r *http.Request) {

	// you can do multiple initializations when debugging
	if ap.initialized && !Debugging {
		msg := "Cannot initialize the action more than once."
		sendError(w, http.StatusForbidden, msg)
		log.Println(msg)
		return
	}

	// read body of the request
	if ap.compiler != "" {
		Debug("compiler: " + ap.compiler)
	}

	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		sendError(w, http.StatusBadRequest, fmt.Sprintf("%v", err))
		return
	}

	// decode request parameters
	if len(body) < 1000 {
		Debug("init: decoding %s\n", string(body))
	}

	var request initRequest
	err = json.Unmarshal(body, &request)

	if err != nil {
		sendError(w, http.StatusBadRequest, fmt.Sprintf("Error unmarshaling request: %v", err))
		return
	}

	// request with empty code - stop any executor but return ok
	if request.Value.Code == "" {
		sendError(w, http.StatusForbidden, "Missing main/no code to execute.")
		return
	}

	// passing the env to the action proxy
	ap.SetEnv(request.Value.Env)

	// setting main
	main := request.Value.Main
	if main == "" {
		main = "main"
	}

	// extract code eventually decoding it
	var buf []byte
	if request.Value.Binary {
		Debug("it is binary code")
		buf, err = base64.StdEncoding.DecodeString(request.Value.Code)
		if err != nil {
			sendError(w, http.StatusBadRequest, "cannot decode the request: "+err.Error())
			return
		}
	} else {
		Debug("it is source code")
		buf = []byte(request.Value.Code)
	}

	// if a compiler is defined try to compile
	binPath, err := ap.ExtractAndCompile(&buf, main)
	if err != nil {
		if os.Getenv("OW_LOG_INIT_ERROR") == "" {
			sendError(w, http.StatusBadGateway, err.Error())
		} else {
			ap.errFile.Write([]byte(err.Error() + "\n"))
			ap.outFile.Write([]byte(OutputGuard))
			ap.errFile.Write([]byte(OutputGuard))
			sendError(w, http.StatusBadGateway, "The action failed to generate or locate a binary. See logs for details.")
		}
		return
	}

	// ** Vast AI
	vastai_api_key, ok := ap.env["OPS_VASTAI_API_KEY"]
	if !ok || vastai_api_key == "" {
		sendError(w, http.StatusBadRequest, "Vast.ai API key not found (use -p OPS_VASTAI_API_KEY api_key when creating the action.)")
		return
	}

	vastai_instance_id, ok := ap.env["OPS_VASTAI_INSTANCE_ID"]
	if !ok || vastai_instance_id == "" {
		sendError(w, http.StatusBadRequest, "Vast.ai Instance ID not found (use -p OPS_VASTAI_INSTANCE_ID instance_id when creating the action.)")
		return
	}

	sshUrl, err := ExtractSSHAddress(vastai_api_key, vastai_instance_id)
	if err != nil {
		sendError(w, http.StatusBadGateway, "Error connecting to Vast.ai. Check logs for details.")
		return
	}
	ap.env["VASTAI_SSH_URL"] = sshUrl
	Debug("Vast.ai SSH URL set: %s", sshUrl)

	vastai_ssh_key, ok := ap.env["OPS_VASTAI_SSH_KEY"]
	if !ok || vastai_instance_id == "" {
		sendError(w, http.StatusBadRequest, "Vast.ai SSH key not found (use -p OPS_VASTAI_SSH_KEY ssh_key when creating the action.)")
		return
	}

	Debug("Some dirs: base %s current %d binpath %s", ap.baseDir, ap.currentDir, binPath)

	sshClient, err := ConnectSSH(sshUrl, vastai_ssh_key)
	if err != nil {
		sendError(w, http.StatusBadGateway, "Error connecting to Vast.ai. Make sure the instance ID and SSH key are set correctly. Check logs for details.")
		return
	}

	err = CreateActionFoldersSSH(sshClient, binPath)
	if err != nil {
		sendError(w, http.StatusBadGateway, "Error creating action folder in Vast.ai. Check logs for details.")
		return
	}

	err = ScpTransfer(sshClient, binPath)
	if err != nil {
		sendError(w, http.StatusBadGateway, "Error transferring action to Vast.ai. Check logs for details.")
		return
	}

	err = UnzipRemote(sshClient, binPath)
	if err != nil {
		sendError(w, http.StatusBadGateway, "Error unzipping action in Vast.ai. Check logs for details.")
		return
	}
	// **

	// NOTE: No need to start the executor in the runtime anymore, since it will be executed in the Vast AI instance
	// start an action
	// err = ap.StartLatestAction()
	// if err != nil {
	// 	if os.Getenv("OW_LOG_INIT_ERROR") == "" {
	// 		sendError(w, http.StatusBadGateway, "cannot start action: "+err.Error())
	// 	} else {
	// 		ap.errFile.Write([]byte(err.Error() + "\n"))
	// 		ap.outFile.Write([]byte(OutputGuard))
	// 		ap.errFile.Write([]byte(OutputGuard))
	// 		sendError(w, http.StatusBadGateway, "Cannot start action. Check logs for details.")
	// 	}
	// 	return
	// }

	ap.initialized = true
	sendOK(w)
}

// ExtractAndCompile decode the buffer and if a compiler is defined, compile it also
func (ap *ActionProxy) ExtractAndCompile(buf *[]byte, main string) (string, error) {

	// extract action in src folder
	file, err := ap.ExtractAction(buf, "src")
	if err != nil {
		return "", err
	}
	if file == "" {
		return "", fmt.Errorf("empty filename")
	}

	// some path surgery
	dir := filepath.Dir(file)
	parent := filepath.Dir(dir)
	srcDir := filepath.Join(parent, "src")
	binDir := filepath.Join(parent, "bin")
	binFile := filepath.Join(binDir, "exec")

	// if the file is already compiled or there is no compiler just move it from src to bin
	if ap.compiler == "" || isCompiled(file) {
		os.Rename(srcDir, binDir)
		return binFile, nil
	}

	// ok let's try to compile
	Debug("compiling: %s main: %s", file, main)
	os.Mkdir(binDir, 0755)
	err = ap.CompileAction(main, srcDir, binDir)
	if err != nil {
		return "", err
	}

	// check only if the file exist
	if _, err := os.Stat(binFile); os.IsNotExist(err) {
		return "", fmt.Errorf("cannot compile")
	}
	return binFile, nil
}
