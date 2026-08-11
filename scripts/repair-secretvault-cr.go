package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: go run ./scripts/repair-secretvault-cr.go /path/to/vault.yaml\n")
		os.Exit(2)
	}

	path := os.Args[1]
	original, err := os.ReadFile(path)
	must(err)
	info, err := os.Stat(path)
	must(err)

	var document yaml.Node
	must(yaml.Unmarshal(original, &document))
	changed := repairBlobs(&document)
	if changed == 0 {
		fmt.Println("No blob values with CR line endings found; file unchanged.")
		return
	}

	backup := path + ".before-cr-repair"
	backupFile, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	must(err)
	if _, err = backupFile.Write(original); err != nil {
		backupFile.Close()
		must(err)
	}
	must(backupFile.Sync())
	must(backupFile.Close())

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	must(encoder.Encode(&document))
	must(encoder.Close())

	temporary, err := os.CreateTemp(filepath.Dir(path), ".secretvault-cr-repair-*")
	must(err)
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	must(temporary.Chmod(info.Mode().Perm()))
	if _, err = temporary.Write(output.Bytes()); err != nil {
		temporary.Close()
		must(err)
	}
	must(temporary.Sync())
	must(temporary.Close())
	must(os.Rename(temporaryName, path))

	fmt.Printf("Repaired %d blob(s). Backup: %s\n", changed, backup)
}

func repairBlobs(node *yaml.Node) int {
	changed := 0
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind == yaml.ScalarNode && key.Value == "blob" && value.Kind == yaml.ScalarNode {
				normalized := strings.ReplaceAll(value.Value, "\r\n", "\n")
				normalized = strings.ReplaceAll(normalized, "\r", "\n")
				if normalized != value.Value {
					value.Value = normalized
					value.Style = yaml.LiteralStyle
					changed++
				}
			}
			changed += repairBlobs(value)
		}
		return changed
	}
	for _, child := range node.Content {
		changed += repairBlobs(child)
	}
	return changed
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
