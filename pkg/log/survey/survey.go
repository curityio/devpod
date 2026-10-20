package survey

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/charmbracelet/huh"
)

// QuestionOptions defines a question and its options
type QuestionOptions struct {
	Question               string
	DefaultValue           string
	DefaultValueSet        bool
	ValidationRegexPattern string
	ValidationMessage      string
	ValidationFunc         func(value string) error
	Options                []string
	Sort                   bool
	IsPassword             bool
}

// DefaultValidationRegexPattern is the default regex pattern to validate the input
var DefaultValidationRegexPattern = regexp.MustCompile("^.*$")

// Survey is the interface for asking questions
type Survey interface {
	Question(params *QuestionOptions) (string, error)
}

type survey struct{}

// NewSurvey creates a new survey object
func NewSurvey() Survey {
	return &survey{}
}

// Question asks the user a question in the terminal and returns the answer. With Options, the
// user picks one of them, otherwise they type the answer, which DefaultValue is used for if empty.
func (s *survey) Question(params *QuestionOptions) (string, error) {
	if len(params.Options) > 0 {
		return selectOption(params)
	}

	compiledRegex := DefaultValidationRegexPattern
	if params.ValidationRegexPattern != "" {
		var err error
		compiledRegex, err = regexp.Compile(params.ValidationRegexPattern)
		if err != nil {
			return "", fmt.Errorf("compile validation pattern: %w", err)
		}
	}

	answer := ""
	input := huh.NewInput().
		Title(params.Question).
		Value(&answer).
		Validate(func(value string) error {
			return validate(params, compiledRegex, withDefault(value, params.DefaultValue))
		})
	if params.IsPassword {
		input = input.EchoMode(huh.EchoModePassword)
	} else if params.DefaultValue != "" {
		input = input.Placeholder(params.DefaultValue)
	}
	if err := input.Run(); err != nil {
		return "", err
	}

	return withDefault(answer, params.DefaultValue), nil
}

func selectOption(params *QuestionOptions) (string, error) {
	options := params.Options
	if params.Sort {
		options = append([]string{}, options...)
		sort.Strings(options)
	}

	answer := params.DefaultValue
	err := huh.NewSelect[string]().
		Title(params.Question).
		Options(huh.NewOptions(options...)...).
		Value(&answer).
		Run()
	if err != nil {
		return "", err
	}
	if answer == "" {
		answer = options[0]
	}

	return answer, nil
}

func validate(params *QuestionOptions, compiledRegex *regexp.Regexp, value string) error {
	if !compiledRegex.MatchString(value) {
		if params.ValidationMessage != "" {
			return errors.New(params.ValidationMessage)
		}

		return fmt.Errorf("answer has to match pattern: %s", compiledRegex.String())
	}

	if params.ValidationFunc != nil {
		if err := params.ValidationFunc(value); err != nil {
			if params.ValidationMessage != "" {
				return errors.New(params.ValidationMessage)
			}

			return err
		}
	}

	return nil
}

func withDefault(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
