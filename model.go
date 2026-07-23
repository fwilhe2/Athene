package main

import (
	"encoding/json"
	"os"
)

// Widget is one design-time component placed on the form. This is athene's
// serialized design-time component entry: absolute position, size, a
// caption, and any wired-up event handlers.
type Widget struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"` // Button, Label, Entry, Box
	X       int               `json:"x"`
	Y       int               `json:"y"`
	W       int               `json:"w"`
	H       int               `json:"h"`
	Caption string            `json:"caption"`
	Signals map[string]string `json:"signals,omitempty"` // event -> handler func name
}

// Form is the whole design surface — the serialized form file.
type Form struct {
	Title   string    `json:"title"`
	Width   int       `json:"width"`
	Height  int       `json:"height"`
	Widgets []*Widget `json:"widgets"`
}

func NewForm() *Form {
	return &Form{Title: "Form1", Width: 480, Height: 360}
}

// defaultSize returns a sensible starting size for a freshly dropped widget.
func defaultSize(typ string) (int, int) {
	switch typ {
	case "Button":
		return 100, 34
	case "Label":
		return 90, 24
	case "Entry":
		return 160, 34
	case "Box":
		return 200, 130
	}
	return 100, 30
}

func (f *Form) Save(path string) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func LoadForm(path string) (*Form, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Form
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}
