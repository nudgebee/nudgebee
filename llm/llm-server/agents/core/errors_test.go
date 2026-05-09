package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWrapperErrors(t *testing.T) {
	e := ErrLlmUnableToGenerate(errors.New("hello"))
	assert.NotNil(t, e)
	assert.ErrorIs(t, e, errLlmUnableToGenerate)
}
