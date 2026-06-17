package app

import "fmt"

const (
	Name            = "aether"
	DefaultNodeHome = ".aether"
)

var ModuleBasics = NewBasicManager()

func New() interface{} {
	fmt.Println("✅ Aether App initialized (skeleton)")
	return nil
}

func NewBasicManager() interface{} {
	return nil
}