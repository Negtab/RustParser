package main

import (
	"math"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

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

type token struct {
	Text    string
	Operand bool // true = операнд, false = оператор
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

	// pendingDeclHeader: true в промежутке между ключевым словом
	// struct/enum/union и её телом ("(" или "{") - нужно, чтобы пометить
	// это тело как isDeclBody (там имена без вызова, а типы полей).
	pendingDeclHeader bool

	// pendingImpl: true между ключевым словом impl и телом "{" (или ";").
	// Если в этом промежутке встретится "for" - это "impl Trait for Type",
	// а не цикл.
	pendingImpl bool

	// pendingName: следующий идентификатор - имя объявляемой сущности
	// (fn/struct/enum/union/trait/type); после него может идти <...>.
	pendingName bool

	// extra: токены, найденные внутри пропущенных дженериков (for<>).
	extra []token

	// pendingAnnot: активна между ключевым словом let/const/static/type
	// и концом возможной аннотации типа - используется, чтобы найти
	// двоеточие/"=" типа на той же глубине скобок, что и само ключевое
	// слово (и не спутать его, например, с двоеточием внутри вложенного
	// паттерна структуры глубже по стеку).
	pendingAnnot pendingAnnotation
}

type pendingAnnotation struct {
	active      bool
	baseDepth   int
	isTypeAlias bool // true для "type X = ..." - тип идёт и после "=" без двоеточия
}

type bracketFrame struct {
	char       byte
	callName   string // непусто для вызова функции/метода ("(") или макроса ("(", "[" или "{")
	isDeclBody bool   // тело struct/enum/union: двоеточия и имя+скобка внутри - объявления полей/вариантов, а не вызовы/литералы
}

func newRustLexer(src []byte) *rustLexer {
	return &rustLexer{src: src, n: len(src), pendingAnnot: pendingAnnotation{baseDepth: -1}}
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

// typeStop описывает условие окончания типового выражения при его пропуске
// функцией skipTypeExpr: набор одиночных символов и/или целых слов
// ("where"), которые на "нулевой" (по отношению к точке входа) глубине
// скобок сигнализируют конец типа.
type typeStop struct {
	chars []byte
	words []string
}

func (l *rustLexer) matchesStop(s typeStop) bool {
	if l.pos >= l.n {
		return true
	}
	c := l.src[l.pos]
	for _, sc := range s.chars {
		if c == sc {
			return true
		}
	}
	for _, w := range s.words {
		if hasPrefix(l.src[l.pos:], w) {
			after := l.peekAt(len(w))
			r, _ := utf8.DecodeRune([]byte{after})
			if !isIdentContinue(r) {
				return true
			}
		}
	}
	return false
}

// skipTypeExpr продвигает l.pos через типовое выражение, НЕ порождая для
// него токенов (ни операторов, ни операндов) - в соответствии с решением
// не считать типы данных вовсе (аналог правила Холстеда для Паскаля,
// где раздел объявлений исключается из подсчёта целиком).
//
// Останавливается на первом символе/слове из stop, встреченном на
// "нулевой" глубине скобок относительно точки входа - вложенные (), [],
// {} и <> внутри самого типа (Vec<HashMap<K, V>>, fn(i32) -> i32,
// &'a [i32; 4] и т.п.) корректно увеличивают/уменьшают эту локальную
// глубину, поэтому не путаются со стоп-символами. Строки/симв. литералы
// и комментарии внутри типа (на практике - в const generics) тоже
// корректно пропускаются, не сбивая подсчёт скобок.
func (l *rustLexer) skipTypeExpr(stop typeStop) {
	depth := 0
	for l.pos < l.n {
		if depth == 0 && l.matchesStop(stop) {
			return
		}
		c := l.src[l.pos]
		switch {
		case c == '/' && l.peekAt(1) == '/':
			l.skipLineComment()
		case c == '/' && l.peekAt(1) == '*':
			l.skipBlockComment()
		case c == '"':
			l.advanceOverStringBody()
		case c == '\'':
			l.lexQuote() // не нужен сам токен - только корректно продвинуть pos
		case c == '(' || c == '{' || c == '[' || c == '<':
			depth++
			l.pos++
		case c == ')' || c == '}' || c == ']' || c == '>':
			if depth == 0 {
				// Несбалансированная скобка на нулевой глубине - выходим,
				// чтобы не застрять и не увести позицию не туда.
				return
			}
			depth--
			l.pos++
		default:
			l.pos++
		}
	}
}

// atHRTB: на текущей позиции стоит слово "for", за которым (после пробелов) идёт "<".
func (l *rustLexer) atHRTB() bool {
	if !hasPrefix(l.src[l.pos:], "for") {
		return false
	}
	if l.pos > 0 {
		if b := l.src[l.pos-1]; isIdentStartByte(b) || isDigit(b) {
			return false // "for" - часть другого идентификатора
		}
	}
	save := l.pos
	l.pos += 3
	p := l.skipSpacesLookahead()
	l.pos = save
	return p < l.n && l.src[p] == '<'
}

// skipGenerics пропускает блок <...>, начиная с '<'. Ничего не считает,
// кроме HRTB: каждый for<...> внутри превращается в токен "for<>".
// "->" внутри (F: Fn() -> i32) не считается закрывающей скобкой.
func (l *rustLexer) skipGenerics() {
	depth := 0
	for l.pos < l.n {
		c := l.src[l.pos]
		switch {
		case c == '-' && l.peekAt(1) == '>':
			l.pos += 2
		case c == '/' && l.peekAt(1) == '/':
			l.skipLineComment()
		case c == '/' && l.peekAt(1) == '*':
			l.skipBlockComment()
		case c == '"':
			l.advanceOverStringBody()
		case c == '\'':
			l.lexQuote()
		case c == 'f' && l.atHRTB():
			l.extra = append(l.extra, token{Text: "for<>", Operand: false})
			l.pos += 3
			l.pos = l.skipSpacesLookahead()
			l.skipGenerics() // пропускаем <'a, ...> рекурсивно
		case c == '<':
			depth++
			l.pos++
		case c == '>':
			depth--
			l.pos++
			if depth == 0 {
				return
			}
		default:
			l.pos++
		}
	}
}

// expectName помечает, что следующий идентификатор - имя объявляемой сущности.
func (l *rustLexer) expectName() {
	p := l.skipSpacesLookahead()
	if p < l.n {
		if r, _ := utf8.DecodeRune(l.src[p:]); isIdentStart(r) {
			l.pendingName = true
		}
	}
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
			tok, emit := l.lexIdentOrMacro()
			tokens = append(tokens, l.extra...)
			l.extra = l.extra[:0]
			if emit {
				tokens = append(tokens, tok)
			}
			continue
		}

		// Открывающая скобка (не поглощённая как часть вызова в
		// lexIdentOrMacro): запоминаем в стеке, токен не выдаём - он
		// будет выдан один раз при встрече закрывающей пары.
		if c == '(' || c == '{' || c == '[' {
			declish := false
			if c == '{' {
				l.pendingImpl = false // заголовок impl закончился
				if l.pendingDeclHeader {
					declish = true
					l.pendingDeclHeader = false
				} else if n := len(l.bracketStack); n > 0 && l.bracketStack[n-1].isDeclBody {
					declish = true
				}
			}
			l.bracketStack = append(l.bracketStack, bracketFrame{char: c, isDeclBody: declish})
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

		// Двоеточие как маркер начала типа: не считаем ни само двоеточие,
		// ни то, что после него, если это (а) аннотация let/const/static/
		// type на той же глубине скобок, что и само ключевое слово,
		// (б) тип параметра прямо внутри списка параметров вызова/функции,
		// или (в) тип поля прямо в теле struct/enum. Двоеточие после
		// лайфтайма ('label: loop {...}) - это метка цикла, а не тип,
		// поэтому исключаем этот случай отдельно.
		if c == ':' && l.peekAt(1) != ':' {
			depth := len(l.bracketStack)
			lastIsLifetimeLabel := len(tokens) > 0 && strings.HasPrefix(tokens[len(tokens)-1].Text, "'")
			isParamColon := depth > 0 && l.bracketStack[depth-1].char == '(' && l.bracketStack[depth-1].callName != ""
			isFieldColon := depth > 0 && l.bracketStack[depth-1].char == '{' && l.bracketStack[depth-1].isDeclBody
			isAnnotColon := l.pendingAnnot.active && l.pendingAnnot.baseDepth == depth
			if !lastIsLifetimeLabel && (isParamColon || isFieldColon || isAnnotColon) {
				l.pendingAnnot.active = false
				l.pos++ // пропускаем ':', не выдаём токен
				var stop typeStop
				switch {
				case isParamColon:
					stop = typeStop{chars: []byte{',', ')'}}
				case isFieldColon:
					stop = typeStop{chars: []byte{',', '}'}}
				default:
					stop = typeStop{chars: []byte{'=', ';'}}
				}
				l.skipTypeExpr(stop)
				continue
			}
		}

		tok := l.lexPunct()
		switch tok.Text {
		case ";":
			tokens = append(tokens, tok)
			l.pendingDeclHeader = false
			l.pendingImpl = false
			if l.pendingAnnot.active && l.pendingAnnot.baseDepth == len(l.bracketStack) {
				l.pendingAnnot.active = false
			}
		case "=":
			tokens = append(tokens, tok)
			if l.pendingAnnot.active && l.pendingAnnot.baseDepth == len(l.bracketStack) {
				wasTypeAlias := l.pendingAnnot.isTypeAlias
				l.pendingAnnot.active = false
				if wasTypeAlias {
					// "type Name = Type;" - двоеточия нет, весь тип идёт
					// сразу после "=" и до ";".
					l.skipTypeExpr(typeStop{chars: []byte{';'}})
				}
			}
		case "->":
			// Возвращаемый тип функции/замыкания/трейт-баунда: сама стрелка -
			// такой же маркер типа, как и ":", поэтому тоже не считается;
			// пропускаем весь тип до тела "{", ";" (сигнатура без тела,
			// напр. в trait) или "where".
			l.skipTypeExpr(typeStop{chars: []byte{'{', ';'}, words: []string{"where"}})
		default:
			tokens = append(tokens, tok)
		}
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
		if text == "else" {
			return token{}, false
		}
		switch text {
		case "impl":
			l.pendingImpl = true
			// impl<T: ...> - пропускаем дженерики сразу после impl
			if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '<' {
				l.pos = p
				l.skipGenerics()
			}
		case "for":
			// HRTB: for<'a> - один оператор "for<>"
			if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '<' {
				l.pos = p
				l.skipGenerics()
				return token{Text: "for<>", Operand: false}, true
			}
			// impl Trait for Type
			if l.pendingImpl {
				l.pendingImpl = false
				return token{Text: "impl-for", Operand: false}, true
			}
		case "fn", "trait":
			l.expectName()
		case "struct", "enum", "union":
			// От этого места и до "(" / "{" тела (или ";" для unit-struct) -
			// заголовок объявления типа: следующее тело нужно пометить как
			// isDeclBody (поля/варианты, а не вызовы/литералы/код).
			l.pendingDeclHeader = true
			l.expectName()
		case "let", "const", "static":
			l.pendingAnnot = pendingAnnotation{active: true, baseDepth: len(l.bracketStack)}
		case "type":
			l.pendingAnnot = pendingAnnotation{active: true, baseDepth: len(l.bracketStack), isTypeAlias: true}
			l.expectName()
		}
		return token{Text: text, Operand: false}, true
	}

	// Имя после fn/struct/enum/union/trait/type: пропускаем <...>,
	// дальше идентификатор идёт по обычному пути (проверка на "(").
	if l.pendingName {
		l.pendingName = false
		if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '<' {
			l.pos = p
			l.skipGenerics()
		}
	}

	// Обычный идентификатор: смотрим вперёд (пропуская только пробелы) -
	// не следует ли за ним "(" вызова. Это покрывает вызовы функций,
	// методов (obj.method()) и конструкторы tuple-структур Foo(...).
	if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '(' {
		l.pos = p + 1 // поглощаем пробелы и открывающую скобку

		// Если это заголовок struct/enum/union (tuple-struct или
		// tuple-вариант перечисления) или мы уже внутри тела struct/enum
		// (isDeclBody) - вся эта "(...)" является списком ТИПОВ полей,
		// а не аргументов вызова: сама пара имя+скобки остаётся одним
		// оператором ("Foo()"), а типы внутри не считаются вовсе.
		declish := l.pendingDeclHeader
		if !declish {
			if n := len(l.bracketStack); n > 0 && l.bracketStack[n-1].isDeclBody {
				declish = true
			}
		}
		l.bracketStack = append(l.bracketStack, bracketFrame{char: '(', callName: text, isDeclBody: declish})
		if declish {
			l.pendingDeclHeader = false
			l.skipTypeExpr(typeStop{chars: []byte{')'}})
		}
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
