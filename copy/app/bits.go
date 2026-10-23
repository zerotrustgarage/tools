package main

import (
	"errors"
	"os"
	"strings"

	"github.com/zerotrustgarage/tools/copy/internal/bits"
	"github.com/zerotrustgarage/tools/copy/internal/private"
)

func (a *App) BitsCatalog() []bits.Entry { return bits.Catalog() }
func (a *App) DefaultBitsJob() bits.Job  { return bits.Default() }
func (a *App) DescribeBits(j bits.Job) bits.View {
	return bits.Describe(j)
}

func (a *App) ImportBitsJob() (*bits.View, error) {
	p, err := a.nativePlatform()
	if err != nil {
		return nil, err
	}
	name, err := p.OpenFile("Import a BITS job JSON file")
	if err != nil || name == "" {
		return nil, err
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	j, err := bits.Decode(file)
	if err != nil {
		return nil, err
	}
	view := bits.Describe(j)
	return &view, nil
}

func (a *App) ExportBitsJob(j bits.Job) (string, error) {
	if _, err := bits.Command(j); err != nil {
		return "", err
	}
	p, err := a.nativePlatform()
	if err != nil {
		return "", err
	}
	name, err := p.SaveFile("bits-transfer.json")
	if err != nil || name == "" {
		return "", err
	}
	return name, private.WriteNew(name, j)
}

func (a *App) ExportBitsScript(j bits.Job) (string, error) {
	view := bits.Describe(j)
	if _, err := bits.Command(j); err != nil {
		return "", err
	}
	p, err := a.nativePlatform()
	if err != nil {
		return "", err
	}
	name, err := p.SaveFile("bits-transfer.ps1")
	if err != nil || name == "" {
		return "", err
	}
	// Windows PowerShell 5.1 needs a UTF-8 BOM to preserve Unicode in scripts.
	data := []byte("\xef\xbb\xbf" + strings.ReplaceAll(view.Script, "\n", "\r\n"))
	return name, private.WriteNewBytes(name, data)
}

type bitsFilePlatform interface {
	TransferFile(string) (string, error)
}

func (a *App) PickBitsFile(kind string) (string, error) {
	if kind != "source" && kind != "destination" {
		return "", errors.New("unknown BITS file endpoint")
	}
	p, err := a.nativePlatform()
	if err != nil {
		return "", err
	}
	files, ok := p.(bitsFilePlatform)
	if !ok {
		return "", errors.New("native file selection is unavailable")
	}
	return files.TransferFile(kind)
}
