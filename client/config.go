package client

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitalautonomy/wahay/config"
)

var (
	errInvalidConfigDirectory = errors.New("invalid client configuration directory")
	errInvalidDataFile        = errors.New("invalid client data file")
	errInvalidConfig          = errors.New("invalid client configuration")

	mumbleFolders = []string{
		"Overlay",
		"Plugins",
		"Themes",
	}
)

func (c *client) EnsureConfiguration() error {
	c.Lock()
	defer c.Unlock()

	binaryDir := c.GetBinaryPath()
	if !isADirectory(binaryDir) {
		binaryDir = filepath.Dir(binaryDir)
	}

	err := createDir(binaryDir)
	if err != nil {
		return errInvalidConfigDirectory
	}

	for _, dir := range mumbleFolders {
		err = createDir(filepath.Join(binaryDir, dir))
		if err != nil {
			log.Printf("Error creating Mumble folder: %s", filepath.Join(binaryDir, dir))
		}
	}

	err = createFile(filepath.Join(binaryDir, configDataName))
	if err != nil {
		log.Println("The Mumble data file could not be created")
	}

	configData := c.databaseProvider()
	err = ioutil.WriteFile(filepath.Join(binaryDir, configDataName), configData, 0644)
	if err != nil {
		return err
	}

	filename := filepath.Join(binaryDir, configFileName)

	err = createFile(filename)
	if err != nil {
		return errInvalidDataFile
	}

	err = c.writeConfigToFile(filename)
	if err != nil {
		return errInvalidConfig
	}

	return nil
}

const (
	configFileName = "mumble.ini"
	configDataName = ".mumble.sqlite"
)

func (c *client) writeConfigToFile(path string) error {
	if len(c.configFile) > 0 && fileExists(c.configFile) {
		_ = os.Remove(c.configFile)
	}

	var configFile string
	if isADirectory(path) {
		configFile = filepath.Join(path, configFileName)
	} else {
		configFile = filepath.Join(filepath.Dir(path), configFileName)
	}

	if !isAFile(configFile) || !fileExists(configFile) {
		err := createFile(configFile)
		if err != nil {
			return errInvalidDataFile
		}
	}

	err := config.SafeWrite(configFile, []byte(c.configContentProvider()), 0600)
	if err != nil {
		return err
	}

	c.configFile = configFile

	return nil
}

func (c *client) saveCertificateConfigFile(cert string) error {
	if len(c.configFile) == 0 || !fileExists(c.configFile) {
		return errors.New("invalid mumble.ini file")
	}

	content, err := ioutil.ReadFile(c.configFile)
	if err != nil {
		return err
	}

	certSectionProp := strings.Replace(
		string(content),
		"#CERTIFICATE",
		fmt.Sprintf("certificate=%s", cert),
		1,
	)

	err = ioutil.WriteFile(c.configFile, []byte(certSectionProp), 0644)
	if err != nil {
		return err
	}

	return nil
}

// f, err := os.Create(filename)
// 	if err != nil {
// 		return err
// 	}

// 	defer f.Close()

// 	_, err = f.Write(data)
// 	if err != nil {
// 		return err
// 	}

// 	log.WithFields(log.Fields{
// 		"filename": filename,
// 	}).Debugf("writePfxDataToFile(): bytes written successfully")

// 	return nil
