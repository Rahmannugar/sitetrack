package request

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/jsonschema-go/jsonschema"
)

var ErrInvalid = errors.New("invalid request body")

type Body[T any] struct {
	Schema    *jsonschema.Schema
	validator *validator.Validate
}

func NewBody[T any]() (*Body[T], error) {
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
		for _, tag := range strings.Split(field.Tag.Get("binding"), ",") {
			key, value, _ := strings.Cut(tag, "=")
			switch key {
			case "required":
				schema.Required = append(schema.Required, name)
			case "min", "max", "len":
				count, err := strconv.Atoi(value)
				if err != nil || property.Type != "string" {
					return nil, errors.New("unsupported request length rule")
				}
				if key == "min" || key == "len" {
					property.MinLength = &count
				}
				if key == "max" || key == "len" {
					property.MaxLength = &count
				}
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
	return &Body[T]{Schema: schema, validator: v}, nil
}

func (b *Body[T]) Decode(c *gin.Context) (T, error) {
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
