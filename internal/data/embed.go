package data

import _ "embed"

//go:embed Ladesaeulenregister_BNetzA_2026-03-25.csv
var registryCSV []byte

// RegistryCSV returns the embedded raw CSV bytes (ISO-8859-1 / Windows-1252).
func RegistryCSV() []byte { return registryCSV }
