package model

import "encoding/json"

// Array has one wire representation for an empty collection: [], never null.
// Keep this guarantee on the DTO itself so API and persisted responses agree.
type Array[T any] []T

func (a Array[T]) MarshalJSON() ([]byte, error) {
	if a == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]T(a))
}
