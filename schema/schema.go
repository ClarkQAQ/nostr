package schema

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"unsafe"

	"fiatjaf.com/nostr"
	"gopkg.in/yaml.v3"
)

const DefaultSchemaURL = "https://raw.githubusercontent.com/nostr-protocol/registry-of-kinds/daaa3a2a5573606aa0a4c4a98c25460ea9a1e388/schema.yaml"

// this is used by hex.Decode in the "hex" validator -- we don't care about data races
var hexdummydecoder = make([]byte, 128)

func fetchSchemaFromURL(schemaURL string) (string, error) {
	resp, err := http.Get(schemaURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch schema from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch schema: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read schema response: %w", err)
	}

	return string(body), nil
}

type Schema struct {
	GenericTags map[string]nextSpec   `yaml:"generic_tags"`
	Kinds       map[string]KindSchema `yaml:"kinds"`
}

type KindSchema struct {
	Content  nextSpec  `yaml:"content"`
	Required []string  `yaml:"required"`
	Tags     []tagSpec `yaml:"tags"`
}

type tagSpec struct {
	Name   string    `yaml:"name"`
	Prefix string    `yaml:"prefix"`
	Next   *nextSpec `yaml:"next"`
}

type nextSpec struct {
	Type     string    `yaml:"type"`
	Required bool      `yaml:"required"`
	Min      int       `yaml:"min"`
	Max      int       `yaml:"max"`
	Either   []string  `yaml:"either"`
	Next     *nextSpec `yaml:"next"`
	Variadic bool      `yaml:"variadic"`
}

type Validator struct {
	Schema            Schema
	FailOnUnknownKind bool
	FailOnUnknownType bool
	TypeValidators    map[string]func(value string, spec *nextSpec) error
	UnknownTypes      []string
}

func NewValidatorFromBytes(schemaData []byte) (Validator, error) {
	schema := Schema{
		GenericTags: make(map[string]nextSpec),
		Kinds:       make(map[string]KindSchema),
	}
	if err := yaml.Unmarshal(schemaData, &schema); err != nil {
		return Validator{}, fmt.Errorf("failed to parse schema: %w", err)
	}

	return NewValidatorFromSchema(schema), nil
}

func NewValidatorFromSchema(sch Schema) Validator {
	validator := Validator{
		Schema: sch,
		TypeValidators: map[string]func(value string, spec *nextSpec) error{
			"id": func(value string, spec *nextSpec) error {
				if len(value) != 64 {
					return fmt.Errorf("needed 64 hex chars")
				}

				_, err := hex.Decode(hexdummydecoder, unsafe.Slice(unsafe.StringData(value), len(value)))
				return err
			},
			"pubkey": func(value string, spec *nextSpec) error {
				_, err := nostr.PubKeyFromHex(value)
				return err
			},
			"addr": func(value string, spec *nextSpec) error {
				_, err := nostr.ParseAddrString(value)
				return err
			},
			"kind": func(value string, spec *nextSpec) error {
				if _, err := strconv.ParseUint(value, 10, 16); err != nil {
					return fmt.Errorf("not an unsigned integer: %w", err)
				}
				return nil
			},
			"relay": func(value string, spec *nextSpec) error {
				if url, err := url.Parse(value); err != nil || (url.Scheme != "ws" && url.Scheme != "wss") {
					return fmt.Errorf("must be ws or wss URL")
				}
				return nil
			},
			"json": func(value string, spec *nextSpec) error {
				if !json.Valid(unsafe.Slice(unsafe.StringData(value), len(value))) {
					return ErrInvalidJson
				}
				return nil
			},
			"constrained": func(value string, spec *nextSpec) error {
				if !slices.Contains(spec.Either, value) {
					return fmt.Errorf("not in allowed list")
				}
				return nil
			},
			"hex": func(value string, spec *nextSpec) error {
				if spec.Min > 0 && len(value) < spec.Min {
					return fmt.Errorf("hex value too short: %d < %d", len(value), spec.Min)
				}
				if spec.Max > 0 && len(value) > spec.Max {
					return fmt.Errorf("hex value too long: %d > %d", len(value), spec.Max)
				}
				_, err := hex.Decode(hexdummydecoder, unsafe.Slice(unsafe.StringData(value), len(value)))
				return err
			},
			"lowercase": func(value string, spec *nextSpec) error {
				if strings.ToLower(value) != value {
					return fmt.Errorf("not lowercase")
				}
				return nil
			},
			"numeric": func(value string, spec *nextSpec) error {
				_, err := strconv.ParseUint(value, 10, 64)
				return err
			},
			"imeta": func(value string, spec *nextSpec) error {
				if len(strings.SplitN(value, " ", 2)) == 2 {
					return nil
				}
				return fmt.Errorf("not a space-separated keyval")
			},
			"empty": func(value string, spec *nextSpec) error {
				if len(value) > 0 {
					return fmt.Errorf("not empty")
				}
				return nil
			},
			"free": func(value string, spec *nextSpec) error {
				return nil // accepts anything
			},
		},
	}

	validator.UnknownTypes = validator.findUnknownTypes(sch)

	return validator
}

func NewValidatorFromFile(filename string) (Validator, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Validator{}, fmt.Errorf("failed to read schema file: %w", err)
	}
	return NewValidatorFromBytes(data)
}

func NewValidatorFromURL(schemaURL string) (Validator, error) {
	schemaData, err := fetchSchemaFromURL(schemaURL)
	if err != nil {
		return Validator{}, err
	}
	return NewValidatorFromBytes([]byte(schemaData))
}

var (
	ErrUnknownContent       = fmt.Errorf("unknown content")
	ErrUnknownKind          = fmt.Errorf("unknown kind")
	ErrInvalidJson          = fmt.Errorf("invalid json")
	ErrEmptyValue           = fmt.Errorf("can't be empty")
	ErrEmptyTag             = fmt.Errorf("empty tag")
	ErrDTagInNonAddressable = fmt.Errorf("non-addressable event can't have a 'd' tag")
	ErrUnknownTagType       = fmt.Errorf("unknown tag type")
	ErrDanglingSpace        = fmt.Errorf("value has dangling space")
)

type UnknownTypes struct {
	Types []string
}

func (ut UnknownTypes) Error() string {
	return fmt.Sprintf("unknown types: %v", ut.Types)
}

type ContentError struct {
	Err error
}

func (ce ContentError) Error() string {
	return fmt.Sprintf("content: %s", ce.Err)
}

type TagError struct {
	Tag  int
	Item int
	Err  error
}

func (te TagError) Error() string {
	return fmt.Sprintf("tag[%d][%d]: %s", te.Tag, te.Item, te.Err)
}

type RequiredTagError struct {
	Missing []string
}

func (rte RequiredTagError) Error() string {
	return fmt.Sprintf("missing tags: %v", rte.Missing)
}

func (v *Validator) ValidateEvent(evt nostr.Event) error {
	if !isTrimmed(evt.Content) {
		return ContentError{ErrDanglingSpace}
	}

	if sch, ok := v.Schema.Kinds[strconv.FormatUint(uint64(evt.Kind), 10)]; ok {
		if validator, ok := v.TypeValidators[sch.Content.Type]; ok {
			if err := validator(evt.Content, &sch.Content); err != nil {
				return ContentError{err}
			}
		} else {
			if v.FailOnUnknownType {
				return ContentError{ErrUnknownContent}
			}
		}

		var requiredTags []string
		if len(sch.Required) > 0 {
			requiredTags = append(requiredTags, sch.Required...)
		}

		needsD := evt.Kind.IsAddressable()

	tags:
		for ti, tag := range evt.Tags {
			if len(tag) == 0 {
				return ErrEmptyTag
			}

			// if this is a "d" tag in an addressable event, just handle it here
			if tag[0] == "d" {
				if evt.Kind.IsAddressable() {
					needsD = false
				} else {
					return ErrDTagInNonAddressable
				}
			}

			if requiredTags != nil {
				// swap-delete this from the list of requirements
				if idx := slices.Index(requiredTags, tag[0]); idx != -1 {
					requiredTags[idx] = requiredTags[len(requiredTags)-1]
					requiredTags = requiredTags[0 : len(requiredTags)-1]
				}
			}

			var lastErr error
			var tagWasValidated bool
		specs:
			for _, tagspec := range sch.Tags {
				if tagspec.Name == tag[0] || (tagspec.Prefix != "" && strings.HasPrefix(tag[0], tagspec.Prefix)) {
					if tagspec.Next != nil {
						tagWasValidated = true
						if ii, err := v.validateNext(tag, 1, tagspec.Next); err != nil {
							lastErr = TagError{ti, ii, err}
							continue specs // see if there is another tagspec that matches this
						} else {
							continue tags
						}
					}
				} else {
					continue specs
				}
			}

			if lastErr != nil {
				// there was at least one failure for this tag and no further successes
				return lastErr
			}

			// when we don't find a specific tag validator for this kind, try a generic one
			if !tagWasValidated {
				if tagSpecNext, ok := v.Schema.GenericTags[tag[0]]; ok {
					if ii, err := v.validateNext(tag, 1, &tagSpecNext); err != nil {
						return TagError{ti, ii, err}
					}
				}
			}
		}

		if len(requiredTags) > 0 {
			return RequiredTagError{requiredTags}
		}

		if needsD {
			return RequiredTagError{Missing: []string{"d"}}
		}
	}

	if v.FailOnUnknownKind {
		return ErrUnknownKind
	}

	return nil
}

func collectTypes(spec *nextSpec, visitedTypes []string, cb func(string)) {
	if spec == nil {
		return
	}
	if !slices.Contains(visitedTypes, spec.Type) {
		visitedTypes = append(visitedTypes, spec.Type)
		cb(spec.Type)
	}

	collectTypes(spec.Next, visitedTypes, cb)
}

func (v *Validator) findUnknownTypes(schema Schema) []string {
	var unknown []string

	visitedTypes := make([]string, 0, 10)
	for _, kindSchema := range schema.Kinds {
		for _, tagSpec := range kindSchema.Tags {
			collectTypes(tagSpec.Next, visitedTypes, func(typeName string) {
				if _, ok := v.TypeValidators[typeName]; !ok {
					unknown = append(unknown, typeName)
				}
			})
		}
	}
	for _, tagSpec := range schema.GenericTags {
		collectTypes(tagSpec.Next, visitedTypes, func(typeName string) {
			if _, ok := v.TypeValidators[typeName]; !ok {
				unknown = append(unknown, typeName)
			}
		})
	}

	return unknown
}

func (v *Validator) validateNext(tag nostr.Tag, index int, this *nextSpec) (failedIndex int, err error) {
	if len(tag) <= index {
		if this.Required {
			return index, fmt.Errorf("invalid tag '%s', missing index %d", tag[0], index)
		}
		return -1, nil
	}

	if !isTrimmed(tag[index]) {
		return index, ErrDanglingSpace
	}

	if validator, ok := v.TypeValidators[this.Type]; ok {
		if err := validator(tag[index], this); err != nil {
			return index, fmt.Errorf("invalid %s value '%s' at tag '%s', index %d: %w",
				this.Type, tag[index], tag[0], index, err)
		}
	} else {
		if v.FailOnUnknownType {
			return index, ErrUnknownTagType
		}
	}

	if this.Variadic {
		// apply this same validation to all further items
		if len(tag) >= index {
			return v.validateNext(tag, index+1, this)
		}
	}

	if this.Next != nil {
		return v.validateNext(tag, index+1, this.Next)
	}

	return -1, nil
}
