package main

import (
	"encoding/json"
	"errors"
	"flag"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
	"os"
)

type Config struct {
	Port       string `json:"port"`
	Version    string `json:"version"`
	PathPrefix string `json:"path_prefix"`
}

func load_config(path string) (*Config, error) {
	file, err := os.Open(path)

	if err != nil {
		return nil, errors.New("Failed to open config file.")
	}

	defer file.Close()

	config := &Config{}

	err = json.NewDecoder(file).Decode(&config)

	if err != nil {
		return nil, errors.New("Failed to parse config file.")
	}

	return config, nil
}

func main() {
	config_file := flag.String("config", "config.json", "path to `config.json`")
	flag.Parse()

	config, err := load_config(*config_file)
	if err != nil {
		log.Printf("Failed to load config.")
		os.Exit(1)
	}

	prefix := config.PathPrefix
	version := config.Version

	r := gin.Default()

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "PAGE_NOT_FOUND",
			"message": "The requested URL was not found on the server.",
			"url":     c.Request.URL.String(),
		})
	})

	r.GET(prefix+"/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": version})
	})

	r.POST(prefix+"/create", handle_create)
	r.POST(prefix+"/join", handle_join)
	r.POST(prefix+"/name", handle_name)
	// Lock a room, preventing new users from joining
	r.POST(prefix+"/complete", handle_complete)
	r.GET(prefix+"/info", handle_info)
	r.GET(prefix+"/events", handle_events)

	r.Run(":" + config.Port)
}
