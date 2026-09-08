package main

import (
	"math"
	"os"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	treesitterrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

// StructuralMetrics хранит сырые данные и итоговые расчеты
type StructuralMetrics struct {
	Operators map[string]int
	Operands  map[string]int

	N1      float64 // Словарь операторов (уникальные)
	N2      float64 // Словарь операндов (уникальные)
	TotalN1 float64 // Общее число операторов
	TotalN2 float64 // Общее число операндов

	Vocabulary float64 // Словарь программы (n)
	Length     float64 // Длина программы (N)
	Volume     float64 // Объем программы (V)
}

// AnalyzeRustFile принимает путь к файлу, парсит его и возвращает заполненную структуру метрик
func AnalyzeRustFile(filePath string) (*StructuralMetrics, error) {
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	parser := treesitter.NewParser()
	defer parser.Close()

	err = parser.SetLanguage(treesitter.NewLanguage(treesitterrust.Language()))
	if err != nil {
		return nil, err
	}

	tree := parser.Parse(sourceCode, nil)
	defer tree.Close()

	rootNode := tree.RootNode()

	res := &StructuralMetrics{
		Operators: make(map[string]int),
		Operands:  make(map[string]int),
	}

	// Запускаем обход дерева
	collectTokens(rootNode, sourceCode, res)

	// Рассчитываем метрики
	res.N1 = float64(len(res.Operators))
	res.N2 = float64(len(res.Operands))

	for _, count := range res.Operators {
		res.TotalN1 += float64(count)
	}
	for _, count := range res.Operands {
		res.TotalN2 += float64(count)
	}

	res.Vocabulary = res.N1 + res.N2
	res.Length = res.TotalN1 + res.TotalN2

	if res.Vocabulary > 0 {
		res.Volume = res.Length * math.Log2(res.Vocabulary)
	}

	return res, nil
}

// Рекурсивный сборщик токенов из AST
func collectTokens(node *treesitter.Node, source []byte, res *StructuralMetrics) {
	if node == nil {
		return
	}
	nodeType := node.Kind()
	nodeText := node.Utf8Text(source)

	if nodeText == "" || nodeType == "line_comment" || nodeType == "block_comment" {
		return
	}

	if node.ChildCount() == 0 {
		switch nodeType {
		case "identifier", "integer_literal", "string_literal", "float_literal", "boolean_literal":
			res.Operands[nodeText]++
		default:
			res.Operators[nodeText]++
		}
	} else {
		if nodeType == "macro_invocation" {
			macroName := node.Child(0).Utf8Text(source) + "!"
			res.Operators[macroName]++
			if node.ChildCount() > 1 {
				collectTokens(node.Child(1), source, res)
			}
			return
		}
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		collectTokens(node.Child(uint(i)), source, res)
	}
}
