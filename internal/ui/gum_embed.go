package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	gumchoose "github.com/charmbracelet/gum/choose"
	gumconfirm "github.com/charmbracelet/gum/confirm"
	gumformat "github.com/charmbracelet/gum/format"
	guminput "github.com/charmbracelet/gum/input"
	gumstyle "github.com/charmbracelet/gum/style"
	gumwrite "github.com/charmbracelet/gum/write"
)

// RunEmbeddedGum executes gum-compatible commands without requiring a host gum binary.
func RunEmbeddedGum(args []string) int {
	err := runEmbeddedGum(args)
	if err == nil {
		return 0
	}

	if code, ok := gumExitCode(err); ok {
		return code
	}

	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	return 1
}

func runEmbeddedGum(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing gum command")
	}

	switch args[0] {
	case "style":
		return runEmbeddedStyle(args[1:])
	case "format":
		return runEmbeddedFormat(args[1:])
	case "choose":
		return runEmbeddedChoose(args[1:])
	case "write":
		return runEmbeddedWrite(args[1:])
	case "input":
		return runEmbeddedInput(args[1:])
	case "confirm":
		return runEmbeddedConfirm(args[1:])
	default:
		return fmt.Errorf("unsupported embedded gum command: %s", args[0])
	}
}

func runEmbeddedStyle(args []string) error {
	opts := gumstyle.Options{
		StripANSI: true,
		Style: gumstyle.StylesNotHidden{
			Align: "left",
		},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--trim":
			opts.Trim = true
		case "--foreground":
			value, err := nextValue(args, &i, "--foreground")
			if err != nil {
				return err
			}
			opts.Style.Foreground = value
		case "--background":
			value, err := nextValue(args, &i, "--background")
			if err != nil {
				return err
			}
			opts.Style.Background = value
		case "--border":
			value, err := nextValue(args, &i, "--border")
			if err != nil {
				return err
			}
			opts.Style.Border = value
		case "--border-foreground":
			value, err := nextValue(args, &i, "--border-foreground")
			if err != nil {
				return err
			}
			opts.Style.BorderForeground = value
		case "--border-background":
			value, err := nextValue(args, &i, "--border-background")
			if err != nil {
				return err
			}
			opts.Style.BorderBackground = value
		case "--align":
			value, err := nextValue(args, &i, "--align")
			if err != nil {
				return err
			}
			opts.Style.Align = value
		case "--height":
			value, err := nextIntValue(args, &i, "--height")
			if err != nil {
				return err
			}
			opts.Style.Height = value
		case "--width":
			value, err := nextIntValue(args, &i, "--width")
			if err != nil {
				return err
			}
			opts.Style.Width = value
		case "--margin":
			value, err := nextValue(args, &i, "--margin")
			if err != nil {
				return err
			}
			opts.Style.Margin = value
		case "--padding":
			value, err := nextValue(args, &i, "--padding")
			if err != nil {
				return err
			}
			opts.Style.Padding = value
		case "--bold":
			opts.Style.Bold = true
		case "--faint":
			opts.Style.Faint = true
		case "--italic":
			opts.Style.Italic = true
		case "--strikethrough":
			opts.Style.Strikethrough = true
		case "--underline":
			opts.Style.Underline = true
		case "--":
			opts.Text = append(opts.Text, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "--") {
				return fmt.Errorf("unsupported style flag: %s", args[i])
			}
			opts.Text = append(opts.Text, args[i])
		}
	}

	return opts.Run()
}

func runEmbeddedFormat(args []string) error {
	opts := gumformat.Options{
		Theme:     "pink",
		Type:      "markdown",
		StripANSI: true,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--theme":
			value, err := nextValue(args, &i, "--theme")
			if err != nil {
				return err
			}
			opts.Theme = value
		case "--language", "-l":
			value, err := nextValue(args, &i, args[i])
			if err != nil {
				return err
			}
			opts.Language = value
		case "--type", "-t":
			value, err := nextValue(args, &i, args[i])
			if err != nil {
				return err
			}
			opts.Type = value
		case "--":
			opts.Template = append(opts.Template, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "--") || strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unsupported format flag: %s", args[i])
			}
			opts.Template = append(opts.Template, args[i])
		}
	}

	return opts.Run()
}

func runEmbeddedChoose(args []string) error {
	opts := gumchoose.Options{
		Limit:            1,
		Height:           10,
		Cursor:           "> ",
		ShowHelp:         true,
		Header:           "Choose:",
		CursorPrefix:     "• ",
		SelectedPrefix:   "✓ ",
		UnselectedPrefix: "• ",
		InputDelimiter:   "\n",
		OutputDelimiter:  "\n",
		StripANSI:        true,
		CursorStyle: gumstyle.Styles{
			Foreground: "212",
		},
		HeaderStyle: gumstyle.Styles{
			Foreground: "99",
		},
		SelectedItemStyle: gumstyle.Styles{
			Foreground: "212",
		},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-limit":
			opts.NoLimit = true
		case "--header":
			value, err := nextValue(args, &i, "--header")
			if err != nil {
				return err
			}
			opts.Header = value
		case "--limit":
			value, err := nextIntValue(args, &i, "--limit")
			if err != nil {
				return err
			}
			opts.Limit = value
		case "--height":
			value, err := nextIntValue(args, &i, "--height")
			if err != nil {
				return err
			}
			opts.Height = value
		case "--cursor":
			value, err := nextValue(args, &i, "--cursor")
			if err != nil {
				return err
			}
			opts.Cursor = value
		case "--cursor-prefix":
			value, err := nextValue(args, &i, "--cursor-prefix")
			if err != nil {
				return err
			}
			opts.CursorPrefix = value
		case "--selected-prefix":
			value, err := nextValue(args, &i, "--selected-prefix")
			if err != nil {
				return err
			}
			opts.SelectedPrefix = value
		case "--unselected-prefix":
			value, err := nextValue(args, &i, "--unselected-prefix")
			if err != nil {
				return err
			}
			opts.UnselectedPrefix = value
		case "--input-delimiter":
			value, err := nextValue(args, &i, "--input-delimiter")
			if err != nil {
				return err
			}
			opts.InputDelimiter = value
		case "--output-delimiter":
			value, err := nextValue(args, &i, "--output-delimiter")
			if err != nil {
				return err
			}
			opts.OutputDelimiter = value
		case "--label-delimiter":
			value, err := nextValue(args, &i, "--label-delimiter")
			if err != nil {
				return err
			}
			opts.LabelDelimiter = value
		case "--":
			opts.Options = append(opts.Options, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "--") || strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unsupported choose flag: %s", args[i])
			}
			opts.Options = append(opts.Options, args[i])
		}
	}

	return opts.Run()
}

func runEmbeddedWrite(args []string) error {
	opts := gumwrite.Options{
		Height:      5,
		Placeholder: "Write something...",
		Prompt:      "┃ ",
		ShowHelp:    true,
		CursorMode:  "blink",
		StripANSI:   true,
		PromptStyle: gumstyle.Styles{Foreground: "7"},
		PlaceholderStyle: gumstyle.Styles{
			Foreground: "240",
		},
		HeaderStyle: gumstyle.Styles{
			Foreground: "240",
		},
		CursorLineNumberStyle: gumstyle.Styles{
			Foreground: "7",
		},
		EndOfBufferStyle: gumstyle.Styles{
			Foreground: "0",
		},
		LineNumberStyle: gumstyle.Styles{
			Foreground: "7",
		},
		CursorStyle: gumstyle.Styles{
			Foreground: "212",
		},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--placeholder":
			value, err := nextValue(args, &i, "--placeholder")
			if err != nil {
				return err
			}
			opts.Placeholder = value
		case "--header":
			value, err := nextValue(args, &i, "--header")
			if err != nil {
				return err
			}
			opts.Header = value
		case "--prompt":
			value, err := nextValue(args, &i, "--prompt")
			if err != nil {
				return err
			}
			opts.Prompt = value
		case "--width":
			value, err := nextIntValue(args, &i, "--width")
			if err != nil {
				return err
			}
			opts.Width = value
		case "--height":
			value, err := nextIntValue(args, &i, "--height")
			if err != nil {
				return err
			}
			opts.Height = value
		case "--":
			opts.Value = strings.Join(args[i+1:], " ")
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "--") || strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unsupported write flag: %s", args[i])
			}
			if opts.Value == "" {
				opts.Value = args[i]
			} else {
				opts.Value += " " + args[i]
			}
		}
	}

	return opts.Run()
}

func runEmbeddedInput(args []string) error {
	opts := guminput.Options{
		Placeholder: "Type something...",
		Prompt:      "> ",
		CursorMode:  "blink",
		CharLimit:   400,
		ShowHelp:    true,
		StripANSI:   true,
		PlaceholderStyle: gumstyle.Styles{
			Foreground: "240",
		},
		CursorStyle: gumstyle.Styles{
			Foreground: "212",
		},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--password":
			opts.Password = true
		case "--placeholder":
			value, err := nextValue(args, &i, "--placeholder")
			if err != nil {
				return err
			}
			opts.Placeholder = value
		case "--prompt":
			value, err := nextValue(args, &i, "--prompt")
			if err != nil {
				return err
			}
			opts.Prompt = value
		case "--value":
			value, err := nextValue(args, &i, "--value")
			if err != nil {
				return err
			}
			opts.Value = value
		case "--header":
			value, err := nextValue(args, &i, "--header")
			if err != nil {
				return err
			}
			opts.Header = value
		case "--width":
			value, err := nextIntValue(args, &i, "--width")
			if err != nil {
				return err
			}
			opts.Width = value
		case "--":
			if opts.Value == "" {
				opts.Value = strings.Join(args[i+1:], " ")
			}
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "--") || strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unsupported input flag: %s", args[i])
			}
			if opts.Value == "" {
				opts.Value = args[i]
			} else {
				opts.Value += " " + args[i]
			}
		}
	}

	return opts.Run()
}

func runEmbeddedConfirm(args []string) error {
	opts := gumconfirm.Options{
		Default:     true,
		Affirmative: "Yes",
		Negative:    "No",
		Prompt:      "Are you sure?",
		ShowHelp:    true,
	}

	var promptParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--show-output":
			opts.ShowOutput = true
		case "--default":
			value, err := nextValue(args, &i, "--default")
			if err != nil {
				return err
			}
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("invalid --default value: %w", err)
			}
			opts.Default = parsed
		case "--affirmative":
			value, err := nextValue(args, &i, "--affirmative")
			if err != nil {
				return err
			}
			opts.Affirmative = value
		case "--negative":
			value, err := nextValue(args, &i, "--negative")
			if err != nil {
				return err
			}
			opts.Negative = value
		case "--prompt":
			value, err := nextValue(args, &i, "--prompt")
			if err != nil {
				return err
			}
			opts.Prompt = value
		case "--":
			promptParts = append(promptParts, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(args[i], "--") || strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unsupported confirm flag: %s", args[i])
			}
			promptParts = append(promptParts, args[i])
		}
	}
	if len(promptParts) > 0 {
		opts.Prompt = strings.Join(promptParts, " ")
	}

	return opts.Run()
}

func nextValue(args []string, i *int, flag string) (string, error) {
	next := *i + 1
	if next >= len(args) {
		return "", fmt.Errorf("%s requires a value", flag)
	}
	*i = next
	return args[next], nil
}

func nextIntValue(args []string, i *int, flag string) (int, error) {
	value, err := nextValue(args, i, flag)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value: %w", flag, err)
	}
	return parsed, nil
}

func gumExitCode(err error) (int, bool) {
	msg := err.Error()
	const prefix = "exit "
	if !strings.HasPrefix(msg, prefix) {
		return 0, false
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(msg, prefix)))
	if convErr != nil {
		return 0, false
	}
	return code, true
}
