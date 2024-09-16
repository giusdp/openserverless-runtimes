package openwhisk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bramvdbogaerde/go-scp"
	"golang.org/x/crypto/ssh"
)

func ExtractSSHAddress(apiKey string, instanceId string) (string, error) {
	httpClient := &http.Client{}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://console.vast.ai/api/v0/instances/%s?owner=me&api_key=%s", instanceId, apiKey), nil)

	if err != nil {
		Debug("[VASTAI] Error creating request: %v", err)
		return "", err
	}

	resp, err := httpClient.Do(req)

	if err != nil {
		Debug("[VASTAI] Error executing request: %v", err)
		return "", err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		Debug("[VASTAI] Error decoding response: %v", err)
		return "", err
	}

	jsonRes := make(map[string]interface{})
	json.Unmarshal(body, &jsonRes)

	instanceJson, ok := jsonRes["instances"].(map[string]interface{})
	if !ok {
		Debug("[VASTAI] Error parsing response: %v", jsonRes)
		return "", errors.New("error parsing vastai reponse")
	}

	ports, ok := instanceJson["ports"].(map[string]interface{})
	if !ok {
		Debug("[VASTAI] Error parsing ports from response: %v", instanceJson)
		return "", errors.New("error retrieving instance ports from vastai")
	}

	port, ok := ports["22/tcp"].([]interface{})[0].(map[string]interface{})["HostPort"]
	if !ok {
		Debug("[VASTAI] Error parsing port from response: %v", ports)
		return "", errors.New("error retrieving instance port from vastai")
	}

	ip := instanceJson["public_ipaddr"]
	return fmt.Sprintf("%s:%s", ip, port), nil
}

func ConnectSSH(sshAddress string, sshPrivateKey string) (*ssh.Client, error) {
	Debug("[VASTAI] Connecting to ssh: %s", sshAddress)
	// create signer for auth
	signer, err := ssh.ParsePrivateKey([]byte(sshPrivateKey))
	if err != nil {
		Debug("[VASTAI] Error parsing private key: %v", err)
		return nil, err
	}

	// define auth method
	auth := ssh.PublicKeys(signer)
	config := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			auth,
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// connect to ssh
	client, err := ssh.Dial("tcp", sshAddress, config)
	if err != nil {
		Debug("[VASTAI] Error connecting to ssh: %v", err)
		return nil, err
	}

	return client, nil
}

func CreateActionFoldersSSH(sshClient *ssh.Client, actionBin string) error {
	Debug("[VASTAI] Creating action folders in Vast.ai instance from %s", actionBin)
	topFolders := strings.Split(actionBin, "/")

	Debug("[VASTAI] Top folders: %s", topFolders)

	session, err := sshClient.NewSession()
	if err != nil {
		Debug("[VASTAI] Error creating new SSH session: %v", err)
		return err
	}

	defer session.Close()

	var errBuffer bytes.Buffer
	session.Stderr = &errBuffer

	// Create the root folders
	err = session.Run(fmt.Sprintf("mkdir -p ~/%s/%s", topFolders[0], topFolders[1]))
	if errBuffer.Len() > 0 {
		Debug("Error creating top action folders in Vast.ai instance: %v", errBuffer.String())
	}
	if err != nil {
		Debug("Error reating top action folders in Vast.ai instance: %v", err)
		return err
	}

	Debug("[VASTAI] Created top folders ~/%s/%s", topFolders[0], topFolders[1])

	result, err := Zip(filepath.Dir(filepath.Dir(actionBin)))
	if err != nil {
		Debug("[VASTAI] Error zipping action: %v", err)
		return err
	}

	err = os.WriteFile(filepath.Join(filepath.Dir(actionBin), "action.zip"), result, 0644)
	if err != nil {
		Debug("[VASTAI] Error writing file: %v", err)
		return err
	}

	return nil
}

func ScpTransfer(sshClient *ssh.Client, actionBin string) error {
	// Create a new SCP client, note that this function might
	// return an error, as a new SSH session is established using the existing connecton

	client, err := scp.NewClientBySSH(sshClient)
	if err != nil {
		Debug("[VASTAI] Error creating new SSH session from existing connection %e", err)
		return err
	}
	defer client.Close()

	actionPath := filepath.Join(filepath.Dir(actionBin), "action.zip")

	// Open the file
	f, err := os.Open(actionPath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", actionPath, err)
	}
	defer f.Close()

	err = client.CopyFromFile(context.Background(), *f, filepath.Join(filepath.Dir(filepath.Dir(actionBin)), "action.zip"), "0655")
	if err != nil {
		Debug("[VASTAI] Error copying file: %v", err)
		return fmt.Errorf("failed to copy file %w", err)
	}

	Debug("[VASTAI] Copied file %s to %s", actionPath, filepath.Dir(filepath.Dir(actionBin)))

	return nil
}

func UnzipRemote(sshClient *ssh.Client, actionBin string) error {
	Debug("[VASTAI] Unzipping file in Vast.ai instance from %s", actionBin)

	session, err := sshClient.NewSession()
	if err != nil {
		Debug("[VASTAI] Error creating new SSH session: %v", err)
		return err
	}

	defer session.Close()

	var errBuffer bytes.Buffer
	session.Stderr = &errBuffer

	// Unzip the file
	// filepath.Dir(filepath.Dir(actionBin)): action/1/bin/exec -> action/1
	cmd := fmt.Sprintf("cd %s && unzip action.zip", filepath.Dir(filepath.Dir(actionBin)))
	err = session.Run(cmd)
	if errBuffer.Len() > 0 {
		Debug("Error unzipping action in Vast.ai instance: %v", errBuffer.String())
	}
	if err != nil {
		Debug("Error unzipping action in Vast.ai instance: %v", err)
		return err
	}

	Debug("[VASTAI] Action unzipped")

	session.Run(fmt.Sprintf("rm ~/%s/action.zip", filepath.Dir(filepath.Dir(actionBin))))

	Debug("[VASTAI] Removed action.zip")

	return nil
}
