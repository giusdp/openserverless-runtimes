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
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

// ErrResponse is the response when there are errors
type ErrResponse struct {
	Error string `json:"error"`
}

func sendError(w http.ResponseWriter, code int, cause string) {
	errResponse := ErrResponse{Error: cause}
	b, err := json.Marshal(errResponse)
	if err != nil {
		b = []byte("error marshalling error response")
		Debug(err.Error())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
	w.Write([]byte("\n"))
}

func (ap *ActionProxy) runHandler(w http.ResponseWriter, r *http.Request) {

	// parse the request
	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		sendError(w, http.StatusBadRequest, fmt.Sprintf("Error reading request body: %v", err))
		return
	}
	Debug("done reading %d bytes", len(body))

	// check if you have an action
	// if ap.theExecutor == nil {
	// 	sendError(w, http.StatusInternalServerError, fmt.Sprintf("no action defined yet"))
	// 	return
	// }
	// // check if the process exited
	// if ap.theExecutor.Exited() {
	// 	sendError(w, http.StatusInternalServerError, fmt.Sprintf("command exited"))
	// 	return
	// }

	// remove newlines
	body = bytes.Replace(body, []byte("\n"), []byte(""), -1)

	// execute the action
	response, err := ap.theExecutor.Interact(body)

	sshUrl := ap.env["VASTAI_SSH_URL"]
	vastai_ssh_key := ap.env["OPS_VASTAI_SSH_KEY"]
	sshClient, err := ConnectSSH(sshUrl, vastai_ssh_key)
	if err != nil {
		sendError(w, http.StatusBadGateway, "Error connecting to Vast.ai. Check logs for details.")
		return
	}

	session, err := sshClient.NewSession()
	if err != nil {
		Debug("Error creating session to Vast.ai instance: %v", err)
		sendError(w, http.StatusBadGateway, "Error creating session to Vast.ai instance. Check logs for details.")
		return
	}
	defer session.Close()

	var outBuffer bytes.Buffer
	session.Stdout = &outBuffer

	var errBuffer bytes.Buffer
	session.Stderr = &errBuffer

	Debug("running action in vast ai with body: %s", string(body))

	// in the instance, the action is in /action and we should run the __main__.py
	err = session.Run(fmt.Sprintf("cd /action && python3 main__.py '%s'", string(body)))
	if errBuffer.Len() > 0 {
		Debug("Error running action in Vast.ai instance: %v", errBuffer.String())
	}
	if err != nil {
		Debug("Error running action in Vast.ai instance: %v", err)
		// check for early termination
		Debug("WARNING! Command exited")
		ap.theExecutor = nil
		sendError(w, http.StatusBadRequest, "command exited")
		return
	}

	// response := outBuffer.Bytes()

	DebugLimit("received:", response, 120)

	// check if the answer is an object map
	var objmap map[string]*json.RawMessage
	var objarray []interface{}
	err = json.Unmarshal(response, &objmap)
	if err != nil {
		err = json.Unmarshal(response, &objarray)
		if err != nil {
			sendError(w, http.StatusBadGateway, "The action did not return a dictionary or array.")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(response)))
	numBytesWritten, err := w.Write(response)

	// flush output
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// diagnostic when you have writing problems
	if err != nil {
		sendError(w, http.StatusInternalServerError, fmt.Sprintf("Error writing response: %v", err))
		return
	}
	if numBytesWritten != len(response) {
		sendError(w, http.StatusInternalServerError, fmt.Sprintf("Only wrote %d of %d bytes to response", numBytesWritten, len(response)))
		return
	}
}
