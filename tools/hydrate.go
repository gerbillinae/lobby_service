package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	filename := os.Args[1]

	flag.Parse()

	log.Println(filename)
	// Specify the template file name

	// Open the template file
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		return
	}
	defer file.Close()

	// Read the file line by line
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		// Replace occurrences of environment variables
		// Environment variables are expected in the format ${VAR_NAME}
		line = replaceEnvVariables(line)
		fmt.Println(line)
	}
}

func replaceEnvVariables(text string) string {
	// Look for occurrences of ${VAR_NAME}
	start := "${"
	end := "}"

	for {
		startIdx := strings.Index(text, start)
		if startIdx == -1 {
			break
		}

		endIdx := strings.Index(text[startIdx:], end)
		if endIdx == -1 {
			break
		}

		// Extract the variable name
		varName := text[startIdx+len(start) : startIdx+endIdx]

		// Get the value of the environment variable
		varValue := os.Getenv(varName)

		// Replace the variable occurrence in the text
		text = strings.Replace(text, start+varName+end, varValue, 1)
	}

	return text
}
