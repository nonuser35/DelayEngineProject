package hotkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	ModifierAlt     uint32 = 0x0001
	ModifierControl uint32 = 0x0002
	ModifierShift   uint32 = 0x0004
	ModifierWin     uint32 = 0x0008

	DefaultArm  = "Ctrl+Alt+D"
	DefaultLive = "Ctrl+Alt+A"
)

type Binding struct {
	Canonical string
	Modifiers uint32
	Key       uint32
}

type Config struct {
	Arm  Binding
	Live Binding
}

func Parse(value string) (Binding, error) {
	parts := strings.Split(strings.TrimSpace(value), "+")
	if len(parts) < 1 || len(parts) > 3 {
		return Binding{}, errors.New("o atalho precisa ter de uma a três teclas")
	}

	var modifiers uint32
	modifierNames := make([]string, 0, 2)
	mainKey := ""
	var key uint32
	for _, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			return Binding{}, errors.New("atalho incompleto")
		}
		switch strings.ToLower(part) {
		case "ctrl", "control":
			if modifiers&ModifierControl != 0 {
				return Binding{}, errors.New("Ctrl aparece mais de uma vez")
			}
			modifiers |= ModifierControl
			modifierNames = append(modifierNames, "Ctrl")
		case "alt":
			if modifiers&ModifierAlt != 0 {
				return Binding{}, errors.New("Alt aparece mais de uma vez")
			}
			modifiers |= ModifierAlt
			modifierNames = append(modifierNames, "Alt")
		case "shift":
			if modifiers&ModifierShift != 0 {
				return Binding{}, errors.New("Shift aparece mais de uma vez")
			}
			modifiers |= ModifierShift
			modifierNames = append(modifierNames, "Shift")
		case "win", "windows":
			if modifiers&ModifierWin != 0 {
				return Binding{}, errors.New("Win aparece mais de uma vez")
			}
			modifiers |= ModifierWin
			modifierNames = append(modifierNames, "Win")
		default:
			if mainKey != "" {
				return Binding{}, errors.New("use apenas uma tecla principal")
			}
			canonical, virtualKey, ok := virtualKeyForName(part)
			if !ok {
				return Binding{}, fmt.Errorf("tecla %q não é compatível", part)
			}
			mainKey = canonical
			key = virtualKey
		}
	}
	if mainKey == "" {
		return Binding{}, errors.New("escolha uma tecla principal além dos modificadores")
	}
	if len(modifierNames) > 2 {
		return Binding{}, errors.New("use no máximo dois modificadores e uma tecla principal")
	}

	canonicalParts := append(modifierNames, mainKey)
	return Binding{
		Canonical: strings.Join(canonicalParts, "+"),
		Modifiers: modifiers,
		Key:       key,
	}, nil
}

func Normalize(value, fallback string) string {
	if binding, err := Parse(value); err == nil {
		return binding.Canonical
	}
	binding, _ := Parse(fallback)
	return binding.Canonical
}

func ValidatePair(armValue, liveValue string) (Config, error) {
	arm, err := Parse(armValue)
	if err != nil {
		return Config{}, fmt.Errorf("atalho para adicionar delay: %w", err)
	}
	live, err := Parse(liveValue)
	if err != nil {
		return Config{}, fmt.Errorf("atalho para voltar ao vivo: %w", err)
	}
	if arm.Modifiers == live.Modifiers && arm.Key == live.Key {
		return Config{}, errors.New("os dois comandos não podem usar o mesmo atalho")
	}
	return Config{Arm: arm, Live: live}, nil
}

func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ValidatePair(DefaultArm, DefaultLive)
		}
		return Config{}, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var settings struct {
		Arm  string `json:"hotkeyArm"`
		Live string `json:"hotkeyLive"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return Config{}, err
	}
	settings.Arm = Normalize(settings.Arm, DefaultArm)
	settings.Live = Normalize(settings.Live, DefaultLive)
	return ValidatePair(settings.Arm, settings.Live)
}

func virtualKeyForName(value string) (string, uint32, bool) {
	name := strings.ToUpper(strings.TrimSpace(value))
	if len(name) == 1 {
		char := name[0]
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			return name, uint32(char), true
		}
	}
	if strings.HasPrefix(name, "F") {
		var number int
		if _, err := fmt.Sscanf(name, "F%d", &number); err == nil && number >= 1 && number <= 24 && number != 12 {
			return fmt.Sprintf("F%d", number), uint32(0x70 + number - 1), true
		}
	}
	keys := map[string]struct {
		name string
		code uint32
	}{
		"SPACE":      {"Space", 0x20},
		"TAB":        {"Tab", 0x09},
		"ESC":        {"Esc", 0x1B},
		"ESCAPE":     {"Esc", 0x1B},
		"INSERT":     {"Insert", 0x2D},
		"DELETE":     {"Delete", 0x2E},
		"HOME":       {"Home", 0x24},
		"END":        {"End", 0x23},
		"PAGEUP":     {"PageUp", 0x21},
		"PAGEDOWN":   {"PageDown", 0x22},
		"UP":         {"Up", 0x26},
		"ARROWUP":    {"Up", 0x26},
		"DOWN":       {"Down", 0x28},
		"ARROWDOWN":  {"Down", 0x28},
		"LEFT":       {"Left", 0x25},
		"ARROWLEFT":  {"Left", 0x25},
		"RIGHT":      {"Right", 0x27},
		"ARROWRIGHT": {"Right", 0x27},
	}
	if key, ok := keys[name]; ok {
		return key.name, key.code, true
	}
	return "", 0, false
}
