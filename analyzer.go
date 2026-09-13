package main

import (
	"math"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// StructuralMetrics хранит сырые данные и итоговые расчеты метрик Холстеда.
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

// AnalyzeRustFile принимает путь к файлу, лексирует его собственным лексером
// (без tree-sitter) и возвращает заполненную структуру метрик.
func AnalyzeRustFile(filePath string) (*StructuralMetrics, error) {
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	res := &StructuralMetrics{
		Operators: make(map[string]int),
		Operands:  make(map[string]int),
	}

	lexer := newRustLexer(sourceCode)
	for _, t := range lexer.Lex() {
		if t.Operand {
			res.Operands[t.Text]++
		} else {
			res.Operators[t.Text]++
		}
	}

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

// ---------------------------------------------------------------------------
// Ручной лексер Rust для подсчета операторов/операндов Холстеда.
// Работает на уровне символов, а не AST, поэтому не зависит от того, как
// конкретная грамматика сворачивает парные токены вроде "{}" или "()".
// ---------------------------------------------------------------------------

type token struct {
	Text    string
	Operand bool // true = операнд (идентификатор, литерал), false = оператор
}

type rustLexer struct {
	src []byte
	pos int
	n   int

	// bracketStack хранит открывающие скобки (, {, [ для сопоставления
	// с закрывающими: пара скобок считается ОДНИМ терминалом-оператором.
	// Если открывающая "(" стоит сразу после имени функции/метода/макроса
	// (вызов), callName хранит это имя - тогда вся пара "имя(...)"
	// схлопывается в один оператор "имя()" вместо отдельного generic "()",
	// как того требует определение операторов Холстеда: "...а также имена
	// процедур и функций".
	bracketStack []bracketFrame
}

type bracketFrame struct {
	char     byte
	callName string // непусто для вызова функции/метода ("(") или макроса ("(", "[" или "{")
}

func newRustLexer(src []byte) *rustLexer {
	return &rustLexer{src: src, n: len(src)}
}

func (l *rustLexer) peek() byte {
	if l.pos >= l.n {
		return 0
	}
	return l.src[l.pos]
}

func (l *rustLexer) peekAt(off int) byte {
	if l.pos+off >= l.n || l.pos+off < 0 {
		return 0
	}
	return l.src[l.pos+off]
}

// skipSpacesLookahead возвращает позицию первого не-пробельного байта начиная
// с текущей, не изменяя l.pos. Используется, чтобы проверить "не идёт ли
// дальше открывающая скобка вызова" перед тем, как решить - потреблять
// пробелы или оставить их основному циклу.
func (l *rustLexer) skipSpacesLookahead() int {
	p := l.pos
	for p < l.n {
		c := l.src[p]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			p++
			continue
		}
		break
	}
	return p
}

// Lex разбирает весь исходный код и возвращает последовательность токенов.
func (l *rustLexer) Lex() []token {
	var tokens []token

	for l.pos < l.n {
		c := l.peek()

		// Пробелы
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			l.pos++
			continue
		}

		// Комментарии
		if c == '/' && l.peekAt(1) == '/' {
			l.skipLineComment()
			continue
		}
		if c == '/' && l.peekAt(1) == '*' {
			l.skipBlockComment()
			continue
		}

		// Байтовая raw-строка: br"..." / br#"..."#
		if tok, ok := l.tryRawByteString(); ok {
			tokens = append(tokens, tok)
			continue
		}
		// Байтовая строка: b"..."
		if tok, ok := l.tryByteString(); ok {
			tokens = append(tokens, tok)
			continue
		}
		// Байтовый символ: b'x'
		if tok, ok := l.tryByteChar(); ok {
			tokens = append(tokens, tok)
			continue
		}
		// Raw-строка r"..."/r#"..."# или raw-идентификатор r#ident
		if tok, ok := l.tryRawStringOrIdent(); ok {
			tokens = append(tokens, tok)
			continue
		}

		if c == '"' {
			tokens = append(tokens, l.lexString())
			continue
		}
		if c == '\'' {
			tokens = append(tokens, l.lexQuote())
			continue
		}
		if isDigit(c) {
			tokens = append(tokens, l.lexNumber())
			continue
		}
		if r, _ := utf8.DecodeRune(l.src[l.pos:]); isIdentStart(r) {
			if tok, emit := l.lexIdentOrMacro(); emit {
				tokens = append(tokens, tok)
			}
			continue
		}

		// Открывающая скобка (не поглощённая как часть вызова в
		// lexIdentOrMacro): запоминаем в стеке, токен не выдаём - он
		// будет выдан один раз при встрече закрывающей пары.
		if c == '(' || c == '{' || c == '[' {
			l.bracketStack = append(l.bracketStack, bracketFrame{char: c})
			l.pos++
			continue
		}
		// Закрывающая скобка: пара (открывающая+закрывающая) считается
		// одним терминалом-оператором, например "()", "{}" или "[]".
		// Если открывающая была частью вызова (callName != ""), выдаём
		// единый оператор "имя()" вместо generic "()".
		if c == ')' || c == '}' || c == ']' {
			l.pos++
			if n := len(l.bracketStack); n > 0 {
				frame := l.bracketStack[n-1]
				l.bracketStack = l.bracketStack[:n-1]
				if frame.callName != "" {
					tokens = append(tokens, token{Text: frame.callName + bracketPairText(frame.char), Operand: false})
				} else {
					tokens = append(tokens, token{Text: bracketPairText(frame.char), Operand: false})
				}
			} else {
				// Нет соответствующей открывающей - некорректный/усечённый
				// код; считаем как одиночный оператор, чтобы не терять токен.
				tokens = append(tokens, token{Text: string(c), Operand: false})
			}
			continue
		}

		tokens = append(tokens, l.lexPunct())
	}

	return tokens
}

func (l *rustLexer) skipLineComment() {
	for l.pos < l.n && l.src[l.pos] != '\n' {
		l.pos++
	}
}

func (l *rustLexer) skipBlockComment() {
	l.pos += 2 // "/*"
	depth := 1
	for l.pos < l.n && depth > 0 {
		if l.src[l.pos] == '/' && l.peekAt(1) == '*' {
			depth++
			l.pos += 2
			continue
		}
		if l.src[l.pos] == '*' && l.peekAt(1) == '/' {
			depth--
			l.pos += 2
			continue
		}
		l.pos++
	}
}

// tryRawByteString разбирает br"..." / br#"..."#.
func (l *rustLexer) tryRawByteString() (token, bool) {
	if l.peek() != 'b' || l.peekAt(1) != 'r' {
		return token{}, false
	}
	start := l.pos
	p := l.pos + 2
	hashes := 0
	for p < l.n && l.src[p] == '#' {
		hashes++
		p++
	}
	if p >= l.n || l.src[p] != '"' {
		return token{}, false
	}
	p++
	closer := "\"" + strings.Repeat("#", hashes)
	if idx := strings.Index(string(l.src[p:]), closer); idx >= 0 {
		l.pos = p + idx + len(closer)
	} else {
		l.pos = l.n
	}
	return token{Text: string(l.src[start:l.pos]), Operand: true}, true
}

// tryByteString разбирает b"...".
func (l *rustLexer) tryByteString() (token, bool) {
	if l.peek() != 'b' || l.peekAt(1) != '"' {
		return token{}, false
	}
	start := l.pos
	l.pos++ // пропускаем 'b', дальше как обычная строка
	l.advanceOverStringBody()
	return token{Text: string(l.src[start:l.pos]), Operand: true}, true
}

// tryByteChar разбирает b'x'.
func (l *rustLexer) tryByteChar() (token, bool) {
	if l.peek() != 'b' || l.peekAt(1) != '\'' {
		return token{}, false
	}
	start := l.pos
	l.pos++ // пропускаем 'b'
	l.advanceOverCharBody()
	return token{Text: string(l.src[start:l.pos]), Operand: true}, true
}

// tryRawStringOrIdent разбирает r"...", r#"..."# или сырой идентификатор r#ident.
func (l *rustLexer) tryRawStringOrIdent() (token, bool) {
	if l.peek() != 'r' {
		return token{}, false
	}
	start := l.pos
	p := l.pos + 1
	hashes := 0
	for p < l.n && l.src[p] == '#' {
		hashes++
		p++
	}

	if p < l.n && l.src[p] == '"' {
		p++
		closer := "\"" + strings.Repeat("#", hashes)
		if idx := strings.Index(string(l.src[p:]), closer); idx >= 0 {
			l.pos = p + idx + len(closer)
		} else {
			l.pos = l.n
		}
		return token{Text: string(l.src[start:l.pos]), Operand: true}, true
	}

	// Сырой идентификатор: ровно один '#' и дальше начало идентификатора.
	if hashes == 1 && p < l.n {
		if r, _ := utf8.DecodeRune(l.src[p:]); isIdentStart(r) {
			q := p
			for q < l.n {
				rr, size := utf8.DecodeRune(l.src[q:])
				if !isIdentContinue(rr) {
					break
				}
				q += size
			}
			l.pos = q
			return token{Text: string(l.src[start:l.pos]), Operand: true}, true
		}
	}

	return token{}, false
}

func (l *rustLexer) advanceOverStringBody() {
	l.pos++ // открывающая кавычка
	for l.pos < l.n {
		c := l.src[l.pos]
		if c == '\\' {
			l.pos += 2
			continue
		}
		if c == '"' {
			l.pos++
			return
		}
		l.pos++
	}
}

func (l *rustLexer) lexString() token {
	start := l.pos
	l.advanceOverStringBody()
	return token{Text: string(l.src[start:l.pos]), Operand: true}
}

func (l *rustLexer) advanceOverCharBody() {
	l.pos++ // открывающая кавычка
	for l.pos < l.n {
		c := l.src[l.pos]
		if c == '\\' {
			l.pos += 2
			continue
		}
		if c == '\'' {
			l.pos++
			return
		}
		if c == '\n' {
			return // некорректный литерал, выходим
		}
		l.pos++
	}
}

// lexQuote разбирает символьный литерал 'x'/'\n' либо лайфтайм 'a/'static.
func (l *rustLexer) lexQuote() token {
	start := l.pos

	if l.peekAt(1) == '\\' {
		p := l.pos + 2
		for p < l.n && l.src[p] != '\'' && l.src[p] != '\n' && p-l.pos < 12 {
			p++
		}
		if p < l.n && l.src[p] == '\'' {
			l.pos = p + 1
			return token{Text: string(l.src[start:l.pos]), Operand: true}
		}
	} else if l.pos+1 < l.n {
		_, size := utf8.DecodeRune(l.src[l.pos+1:])
		q := l.pos + 1 + size
		if q < l.n && l.src[q] == '\'' {
			l.pos = q + 1
			return token{Text: string(l.src[start:l.pos]), Operand: true}
		}
	}

	// Не символьный литерал -> лайфтайм ('a, 'static) или одинокий тик.
	p := l.pos + 1
	for p < l.n {
		r, size := utf8.DecodeRune(l.src[p:])
		if !isIdentContinue(r) {
			break
		}
		p += size
	}
	if p == l.pos+1 {
		l.pos++
		return token{Text: "'", Operand: false}
	}
	l.pos = p
	return token{Text: string(l.src[start:l.pos]), Operand: false}
}

// lexNumber разбирает целые/дробные литералы, включая 0x/0o/0b, экспоненту и суффиксы.
func (l *rustLexer) lexNumber() token {
	start := l.pos

	switch {
	case l.peek() == '0' && (l.peekAt(1) == 'x' || l.peekAt(1) == 'X'):
		l.pos += 2
		for l.pos < l.n && (isHexDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
			l.pos++
		}
	case l.peek() == '0' && (l.peekAt(1) == 'o' || l.peekAt(1) == 'O'):
		l.pos += 2
		for l.pos < l.n && (isOctDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
			l.pos++
		}
	case l.peek() == '0' && (l.peekAt(1) == 'b' || l.peekAt(1) == 'B'):
		l.pos += 2
		for l.pos < l.n && (l.src[l.pos] == '0' || l.src[l.pos] == '1' || l.src[l.pos] == '_') {
			l.pos++
		}
	default:
		for l.pos < l.n && (isDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
			l.pos++
		}
		// Дробная часть: не трогаем ".." (range) и "1.method()" (доступ к полю/методу).
		if l.pos < l.n && l.src[l.pos] == '.' &&
			l.peekAt(1) != '.' && !isIdentStartByte(l.peekAt(1)) {
			l.pos++
			for l.pos < l.n && (isDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
				l.pos++
			}
		}
		// Экспонента.
		if l.pos < l.n && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
			save := l.pos
			p := l.pos + 1
			if p < l.n && (l.src[p] == '+' || l.src[p] == '-') {
				p++
			}
			if p < l.n && isDigit(l.src[p]) {
				l.pos = p
				for l.pos < l.n && (isDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
					l.pos++
				}
			} else {
				l.pos = save
			}
		}
	}

	// Суффикс типа (u32, i64, usize, f64, ...).
	for l.pos < l.n {
		r, size := utf8.DecodeRune(l.src[l.pos:])
		if !isIdentContinue(r) {
			break
		}
		l.pos += size
	}

	return token{Text: string(l.src[start:l.pos]), Operand: true}
}

// lexIdentOrMacro разбирает идентификатор/ключевое слово, макро-вызов ident!,
// либо вызов функции/метода ident(...).
//
// Возвращает (token, true), если токен нужно сразу добавить в поток, либо
// (_, false), если идентификатор оказался именем вызова: открывающая "("
// уже поглощена и помещена в bracketStack с этим именем - итоговый
// оператор "имя()" будет выдан позже, при встрече закрывающей скобки
// (см. основной цикл Lex). Это отражает определение Холстеда: имя
// процедуры/функции - оператор, а пара скобок вызова не считается
// отдельно (иначе она задвоила бы счёт).
func (l *rustLexer) lexIdentOrMacro() (token, bool) {
	start := l.pos
	for l.pos < l.n {
		r, size := utf8.DecodeRune(l.src[l.pos:])
		if !isIdentContinue(r) {
			break
		}
		l.pos += size
	}
	text := string(l.src[start:l.pos])

	// Макро-вызов: ident! (но не "!="). У макросов Rust разделителем
	// аргументов может быть "(", "[" или "{" (println!(...), vec![...],
	// lazy_static!{...}) - схлопываем имя+"!" вместе с этой скобкой в один
	// оператор, по той же логике, что и для обычных вызовов функций.
	if l.peek() == '!' && l.peekAt(1) != '=' {
		l.pos++
		macroName := text + "!"
		if p := l.skipSpacesLookahead(); p < l.n {
			switch l.src[p] {
			case '(', '[', '{':
				open := l.src[p]
				l.pos = p + 1
				l.bracketStack = append(l.bracketStack, bracketFrame{char: open, callName: macroName})
				return token{}, false
			}
		}
		// Разделитель не найден сразу (например, макрос без тела) -
		// просто выдаём имя макроса как самостоятельный оператор.
		return token{Text: macroName, Operand: false}, true
	}

	if text == "true" || text == "false" {
		return token{Text: text, Operand: true}, true
	}
	if rustKeywords[text] {
		// Ключевые слова (if, while, match...) сами по себе операторы;
		// круглая скобка после них (например "if (x)") - обычная
		// группирующая скобка, а не вызов, поэтому имя с ней не сливаем.
		return token{Text: text, Operand: false}, true
	}

	// Обычный идентификатор: смотрим вперёд (пропуская только пробелы) -
	// не следует ли за ним "(" вызова. Это покрывает вызовы функций,
	// методов (obj.method()) и конструкторы tuple-структур Foo(...).
	if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '(' {
		l.pos = p + 1 // поглощаем пробелы и открывающую скобку
		l.bracketStack = append(l.bracketStack, bracketFrame{char: '(', callName: text})
		return token{}, false
	}

	return token{Text: text, Operand: true}, true
}

var threeCharOps = []string{"<<=", ">>=", "..=", "..."}

var twoCharOps = []string{
	"::", "->", "=>", "==", "!=", "<=", ">=", "&&", "||",
	"+=", "-=", "*=", "/=", "%=", "^=", "&=", "|=", "<<", ">>", "..",
}

// bracketPairText возвращает канонический текст терминала для пары скобок.
// Угловые скобки <> намеренно не входят сюда: символ '<'/'>' неотделим на
// уровне лексера от операторов сравнения (< > <= >=), поэтому их надёжно
// сопоставить без разбора грамматики нельзя - они остаются одиночными
// операторами, как обычные знаки сравнения.
func bracketPairText(open byte) string {
	switch open {
	case '(':
		return "()"
	case '{':
		return "{}"
	case '[':
		return "[]"
	}
	return string(open)
}

// lexPunct разбирает операторы и пунктуацию (кроме (){}[] - те сворачиваются
// в единый терминал парой ещё в основном цикле Lex).
func (l *rustLexer) lexPunct() token {
	remaining := l.src[l.pos:]

	for _, op := range threeCharOps {
		if hasPrefix(remaining, op) {
			l.pos += len(op)
			return token{Text: op, Operand: false}
		}
	}
	for _, op := range twoCharOps {
		if hasPrefix(remaining, op) {
			l.pos += len(op)
			return token{Text: op, Operand: false}
		}
	}

	r, size := utf8.DecodeRune(remaining)
	l.pos += size
	return token{Text: string(r), Operand: false}
}

func hasPrefix(b []byte, s string) bool {
	if len(b) < len(s) {
		return false
	}
	return string(b[:len(s)]) == s
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isOctDigit(c byte) bool { return c >= '0' && c <= '7' }

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentContinue(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isIdentStartByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// rustKeywords - ключевые слова Rust (строгие + зарезервированные), кроме
// true/false (считаются литералами-операндами) и self/Self (считаются
// операндами, так как обозначают значение/тип, а не управляющую конструкцию).
var rustKeywords = map[string]bool{
	"as": true, "async": true, "await": true, "break": true,
	"const": true, "continue": true, "crate": true, "dyn": true,
	"else": true, "enum": true, "extern": true, "fn": true, "for": true,
	"if": true, "impl": true, "in": true, "let": true, "loop": true,
	"match": true, "mod": true, "move": true, "mut": true, "pub": true,
	"ref": true, "return": true, "static": true, "struct": true,
	"super": true, "trait": true, "type": true, "unsafe": true,
	"use": true, "where": true, "while": true,
	// зарезервированные / контекстные
	"abstract": true, "become": true, "box": true, "do": true,
	"final": true, "macro": true, "override": true, "priv": true,
	"typeof": true, "unsized": true, "virtual": true, "yield": true,
	"try": true, "union": true,
}
