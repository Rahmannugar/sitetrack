package request

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/jsonschema-go/jsonschema"
)

var ErrInvalid = errors.New("invalid request body")

type Contract[T any] struct {
	Schema    *jsonschema.Schema
	validator *validator.Validate
}

func NewBody[T any]() (*Contract[T], error) { return NewContract[T]() }

func NewContract[T any]() (*Contract[T], error) {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		return nil, err
	}
	typeOf := reflect.TypeFor[T]()
	if typeOf.Kind() != reflect.Struct {
		return nil, errors.New("request DTO must be a struct")
	}
	schema.Required = nil
	for i := range typeOf.NumField() {
		field := typeOf.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		property := schema.Properties[name]
		if property == nil {
			continue
		}
		formOptions := strings.Split(field.Tag.Get("form"), ",")
		for _, option := range formOptions[1:] {
			key, value, _ := strings.Cut(option, "=")
			if key == "default" {
				if property.Type == "integer" {
					number, err := strconv.Atoi(value)
					if err != nil {
						return nil, err
					}
					property.Default, _ = json.Marshal(number)
				} else {
					property.Default, _ = json.Marshal(value)
				}
			}
		}
		for _, tag := range strings.Split(field.Tag.Get("binding"), ",") {
			key, value, _ := strings.Cut(tag, "=")
			switch key {
			case "required":
				property.Types = slices.DeleteFunc(property.Types, func(value string) bool { return value == "null" })
				if len(property.Types) == 1 {
					property.Type = property.Types[0]
					property.Types = nil
				}
				schema.Required = append(schema.Required, name)
			case "min", "max", "len":
				count, err := strconv.Atoi(value)
				if err != nil {
					return nil, errors.New("unsupported request length rule")
				}
				if property.Type == "integer" && key != "len" {
					number := float64(count)
					if key == "min" {
						property.Minimum = &number
					} else {
						property.Maximum = &number
					}
				} else if property.Type == "string" {
					if key == "min" || key == "len" {
						property.MinLength = &count
					}
					if key == "max" || key == "len" {
						property.MaxLength = &count
					}
				} else {
					return nil, errors.New("unsupported request length rule")
				}
			case "datetime":
				if value != "2006-01-02" {
					return nil, errors.New("unsupported request date format")
				}
				property.Format = "date"
				property.Pattern = `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`
			case "uuid":
				property.Format = "uuid"
			case "email":
				property.Format = "email"
			case "number":
				property.Pattern = "^[0-9]+$"
			case "oneof":
				values := strings.Fields(value)
				for i := range values {
					values[i] = regexp.QuoteMeta(values[i])
				}
				property.Pattern = "^(" + strings.Join(values, "|") + ")$"
			case "", "omitempty":
			default:
				return nil, errors.New("unsupported request validation rule: " + key)
			}
		}
	}
	v := validator.New()
	v.SetTagName("binding")
	return &Contract[T]{Schema: schema, validator: v}, nil
}

func (b *Contract[T]) Decode(c *gin.Context) (T, error) {
	var value T
	if c.ContentType() != "application/json" {
		return value, ErrInvalid
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, ErrInvalid
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return value, ErrInvalid
	}
	if err := b.validator.Struct(value); err != nil {
		return value, ErrInvalid
	}
	return value, nil
}

func (b *Contract[T]) DecodeQuery(c *gin.Context) (T, error) {
	var value T
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return value, ErrInvalid
	}
	for name, values := range query {
		if b.Schema.Properties[name] == nil || len(values) != 1 {
			return value, ErrInvalid
		}
	}
	if err := c.ShouldBindQuery(&value); err != nil {
		return value, ErrInvalid
	}
	if err := b.validator.Struct(value); err != nil {
		return value, ErrInvalid
	}
	return value, nil
}

func (b *Contract[T]) DecodePath(c *gin.Context) (T, error) {
	var value T
	if err := c.ShouldBindUri(&value); err != nil {
		return value, ErrInvalid
	}
	if err := b.validator.Struct(value); err != nil {
		return value, ErrInvalid
	}
	return value, nil
}
