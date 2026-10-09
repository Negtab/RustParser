package main

import (
	"math"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type StructuralMetrics struct {
	Operators map[string]int
	Operands  map[string]int

	N1      float64
	N2      float64
	TotalN1 float64
	TotalN2 float64

	Vocabulary float64
	Length     float64
	Volume     float64

	Gilb     GilbMetrics
	Boundary BoundaryMetrics
}

// GilbMetrics - метрики Джилба: сложность программы через число управляющих
// конструкций (циклов и ветвлений, включая многовариантный выбор match)
// и глубину их вложенности.
type GilbMetrics struct {
	Absolute   int     // AC: число циклов, ветвлений и вариантов match
	Relative   float64 // OC: AC / TotalN1 (общее число операторов Холстеда)
	Count      int
	MaxNesting int            // максимальный уровень вложенности управляющих конструкций
	Kinds      map[string]int // разбивка AC по видам: if, for, while, loop, match (ветви)
}

// BoundaryMetrics - метрика граничных значений: абсолютная (Sa) и
// относительная (So) граничная сложность, посчитанные по упрощённому
// графу потока управления программы (см. комментарии у cfNode).
type BoundaryMetrics struct {
	Absolute float64 // Sa - сумма скорректированных сложностей всех вершин
	Relative float64 // So = (Sa - 1) / (ν - 1)
	Vertices int      // ν - общее число вершин графа программы
}

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

	res.Gilb = lexer.gilb
	refOps := 0
	for opText, count := range res.Operators {
		if referenceOperators[opText] {
			refOps += count
		}
	}
	if refOps > 0 {
		res.Gilb.Count = refOps + res.Gilb.Absolute
		res.Gilb.Relative = float64(res.Gilb.Absolute) / (float64(refOps) + float64(res.Gilb.Absolute))
	}

	res.Boundary = computeBoundary(lexer.cfRoots)

	return res, nil
}

type token struct {
	Text    string
	Operand bool
}

// controlPending - управляющая конструкция (if/while/for/loop/match),
// заголовок которой уже разобран, а тело "{" ещё не встречено.
// depth - глубина стека скобок в момент ключевого слова: тело этой
// конструкции откроется на той же глубине.
type controlPending struct {
	depth int
	kind  byte // 'o' - if/while/for/loop, 'm' - match
}

// --- метрика граничных значений: упрощённый граф потока управления ---
//
// Для каждой функции строится граф на уровне управляющих конструкций, а
// не отдельных машинных инструкций. Прямолинейный участок кода без
// ветвлений - принимающая вершина (AC=1). if/while/for/match - вершина
// выбора; её скорректированная сложность = сумма числа вершин в каждой
// исходящей ветви (пустая ветвь, например "if без else" или "выход из
// цикла", даёт 0) плюс 1 за общую нижнюю границу (точку, в которой
// ветви сходятся). Конечная вершина функции имеет сложность 0.
// loop/break/continue сами по себе не ветвятся (ветвление создаёт
// только условный break, т.е. вложенный if) и поэтому не порождают
// отдельной вершины выбора.
type cfNode struct {
	kind     byte // 'r' - принимающая, 's' - выбора, 'e' - конечная
	branches [][]*cfNode
}

func (n *cfNode) adjustedComplexity() int {
	switch n.kind {
	case 'r':
		return 1
	case 'e':
		return 0
	default: // 's'
		total := 1 // нижняя граница подграфа (точка схождения ветвей)
		for _, branch := range n.branches {
			total += len(branch)
		}
		return total
	}
}

// cfBlockBuilder накапливает прямолинейный код внутри одной ветви
// (тело функции, then/else-ветвь if, тело while/for, тело ветви match).
type cfBlockBuilder struct {
	nodes       []*cfNode
	pendingCode bool
}

func (b *cfBlockBuilder) flush() {
	if b.pendingCode {
		b.nodes = append(b.nodes, &cfNode{kind: 'r'})
		b.pendingCode = false
	}
}

// cfSelFrame - открытая вершина выбора if/while/for, ждущая своей
// (первой, а для циклов - и единственной) ветви.
type cfSelFrame struct {
	node     *cfNode
	ctorKind byte // 'i' - if (может быть else/else-if), 'w' - while/for (вторая ветвь - пустой выход)
}

// computeBoundary обходит построенные графы всех функций и считает
// Sa, ν и So.
func computeBoundary(roots [][]*cfNode) BoundaryMetrics {
	var all []*cfNode
	var walk func(block []*cfNode)
	walk = func(block []*cfNode) {
		for _, n := range block {
			all = append(all, n)
			for _, branch := range n.branches {
				walk(branch)
			}
		}
	}
	for _, root := range roots {
		walk(root)
	}

	sa := 0
	for _, n := range all {
		sa += n.adjustedComplexity()
	}
	v := len(all)

	m := BoundaryMetrics{Absolute: float64(sa), Vertices: v}
	if v > 1 {
		m.Relative = float64(sa-1) / float64(v-1)
	}
	return m
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

type rustLexer struct {
	src []byte
	pos int
	n   int

	bracketStack []bracketFrame // для открывающих скобок

	pendingDeclHeader bool
	pendingImpl       bool
	pendingName       bool

	extra []token

	rootPure bool
	seen     int

	headerDepth  int
	pendingAnnot pendingAnnotation

	// pendingControls: управляющие конструкции, ждущие своего тела "{".
	pendingControls []controlPending
	// matchBodyDepths: глубины, на которых лежат ветви открытых match
	// (по одной на каждый вложенный match).
	matchBodyDepths []int
	// currentNesting: текущая вложенность управляющих конструкций.
	currentNesting int
	gilb           GilbMetrics

	// --- метрика граничных значений (boundary value metric) ---
	pendingFnBody      bool              // между "fn" и открывающей "{" тела функции
	cfStack            []*cfBlockBuilder // стек строящихся блоков (тело функции / открытая ветвь)
	cfSelStack         []*cfSelFrame     // стек открытых вершин выбора if/while/for
	cfMatchStack       []*cfNode         // стек открытых вершин match
	cfMatchDepth       []int             // глубина cfStack на момент открытия каждого match
	cfElseIfChain      []*cfNode         // if, ждущие, пока вложенный if (else-if) не присоединится как их else-ветвь
	cfAwaitingElse     *cfNode           // if, чья then-ветвь закрылась и далее идёт "else {"
	cfElseBodyFor      []*cfNode         // владельцы открытых блоков "else { ... }"
	cfAwaitingMatchArm bool              // сразу после "=>" обнаружено "{" - следующий блок это тело ветви match
	cfRoots            [][]*cfNode       // завершённые графы (по одному на каждую функцию верхнего уровня)
}

type pendingAnnotation struct {
	active      bool
	baseDepth   int
	isTypeAlias bool
}

type bracketFrame struct {
	char       byte
	callName   string
	isDeclBody bool
	pure       bool
	stmtLevel  bool
	isDecl     bool

	// isControlBody: эта "{" - тело if/while/for/loop/match (не функции,
	// не impl, не литерала). controlKind хранит, какого именно вида.
	isControlBody bool
	controlKind   byte

	// --- метрика граничных значений ---
	cfFnBody    bool // эта "{" - тело функции (корень графа для Sa/ν)
	cfElseClose bool // эта "{" - блок "else { ... }" без "if"
	cfArmClose  bool // эта "{" - тело ветви match, оформленное как блок
}

func newRustLexer(src []byte) *rustLexer {
	return &rustLexer{
		src:          src,
		n:            len(src),
		rootPure:     true,
		headerDepth:  -1,
		pendingAnnot: pendingAnnotation{baseDepth: -1},
		gilb:         GilbMetrics{Kinds: make(map[string]int)},
	}
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

// wordAt проверяет, что начиная с позиции p в исходнике стоит слово word
// целиком (не как префикс более длинного идентификатора).
func (l *rustLexer) wordAt(p int, word string) bool {
	if !hasPrefix(l.src[p:], word) {
		return false
	}
	after := p + len(word)
	if after >= l.n {
		return true
	}
	r, _ := utf8.DecodeRune(l.src[after:])
	return !isIdentContinue(r)
}

func (l *rustLexer) curPure() bool {
	if n := len(l.bracketStack); n > 0 {
		return l.bracketStack[n-1].pure
	}
	return l.rootPure
}

func (l *rustLexer) setPure(v bool) {
	if n := len(l.bracketStack); n > 0 {
		l.bracketStack[n-1].pure = v
	} else {
		l.rootPure = v
	}
}

func (l *rustLexer) notePure(t token) {
	switch {
	case t.Text == ";" || t.Text == "{}":
		l.setPure(true)
	case t.Operand, t.Text == ".", t.Text == "::", t.Text == "?",
		strings.HasSuffix(t.Text, "()"), strings.HasSuffix(t.Text, "[]"),
		strings.HasSuffix(t.Text, "{}"):
	default:
		l.setPure(false)
	}
}

// markCfCode отмечает, что в текущем открытом блоке (для метрики
// граничных значений) появился обычный код - он станет принимающей
// вершиной при закрытии блока. Токены внутри заголовка конструкции
// (условие if/while, список match) и паттерны/простые тела ветвей
// match, ещё не оформленные как отдельный блок, не считаются.
func (l *rustLexer) markCfCode(t token) {
	if len(l.cfStack) == 0 {
		return
	}
	if l.headerDepth != -1 {
		return
	}
	if t.Text == "" {
		return
	}
	if n := len(l.cfMatchDepth); n > 0 && len(l.cfStack) == l.cfMatchDepth[n-1] {
		return
	}
	l.cfStack[len(l.cfStack)-1].pendingCode = true
}

// attachSelNode присоединяет завершённую вершину выбора туда, где её
// место: если это if, ожидаемый в роли else-ветви предыдущего if
// (цепочка else-if), то он становится содержимым этой ветви, и тогда уже
// внешний if считается завершённым и присоединяется тем же путём
// рекурсивно; иначе узел добавляется в текущий открытый блок.
func (l *rustLexer) attachSelNode(node *cfNode) {
	if n := len(l.cfElseIfChain); n > 0 {
		outer := l.cfElseIfChain[n-1]
		l.cfElseIfChain = l.cfElseIfChain[:n-1]
		outer.branches = append(outer.branches, []*cfNode{node})
		l.attachSelNode(outer)
		return
	}
	if n := len(l.cfStack); n > 0 {
		l.cfStack[n-1].nodes = append(l.cfStack[n-1].nodes, node)
	}
}

func (l *rustLexer) isPatternEnd(p int) bool {
	return p+1 < l.n && l.src[p] == '=' && l.src[p+1] != '='
}

type typeStop struct {
	chars []byte
	words []string
}

func (l *rustLexer) matchesStop(s typeStop) bool {
	if l.pos >= l.n {
		return true
	}
	c := l.src[l.pos]
	if slices.Contains(s.chars, c) {
		return true
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
			l.lexQuote()
		case c == '(' || c == '{' || c == '[' || c == '<':
			depth++
			l.pos++
		case c == ')' || c == '}' || c == ']' || c == '>':
			if depth == 0 {
				return
			}
			depth--
			l.pos++
		default:
			l.pos++
		}
	}
}

func (l *rustLexer) atHRTB() bool {
	if !hasPrefix(l.src[l.pos:], "for") {
		return false
	}
	if l.pos > 0 {
		if b := l.src[l.pos-1]; isIdentStartByte(b) || isDigit(b) {
			return false
		}
	}
	save := l.pos
	l.pos += 3
	p := l.skipSpacesLookahead()
	l.pos = save
	return p < l.n && l.src[p] == '<'
}

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
			l.skipGenerics()
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

func (l *rustLexer) skipTypeDecl() {
	l.pos = l.skipSpacesLookahead()
	for l.pos < l.n {
		r, size := utf8.DecodeRune(l.src[l.pos:])
		if !isIdentContinue(r) {
			break
		}
		l.pos += size
	}

	if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '<' {
		l.pos = p
		keep := len(l.extra)
		l.skipGenerics()
		l.extra = l.extra[:keep]
	}

	l.skipTypeExpr(typeStop{chars: []byte{'{', ';'}})

	if l.pos >= l.n {
		return
	}
	if l.src[l.pos] == ';' {
		l.pos++
		return
	}

	depth := 0
	for l.pos < l.n {
		c := l.src[l.pos]
		switch {
		case c == '/' && l.peekAt(1) == '/':
			l.skipLineComment()
		case c == '/' && l.peekAt(1) == '*':
			l.skipBlockComment()
		case c == '"':
			l.advanceOverStringBody()
		case c == '\'':
			l.lexQuote()
		case c == '{':
			depth++
			l.pos++
		case c == '}':
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

func (l *rustLexer) expectName() {
	p := l.skipSpacesLookahead()
	if p < l.n {
		if r, _ := utf8.DecodeRune(l.src[p:]); isIdentStart(r) {
			l.pendingName = true
		}
	}
}

func (l *rustLexer) Lex() []token {
	var tokens []token

	for l.pos < l.n {

		if len(tokens) > l.seen {
			for _, t := range tokens[l.seen:] {
				l.notePure(t)
				l.markCfCode(t)
			}
			l.seen = len(tokens)
		}

		c := l.peek()

		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			l.pos++
			continue
		}

		if c == '/' && l.peekAt(1) == '/' {
			l.skipLineComment()
			continue
		}
		if c == '/' && l.peekAt(1) == '*' {
			l.skipBlockComment()
			continue
		}

		if tok, ok := l.tryRawByteString(); ok {
			tokens = append(tokens, tok)
			continue
		}
		if tok, ok := l.tryByteString(); ok {
			tokens = append(tokens, tok)
			continue
		}
		if tok, ok := l.tryByteChar(); ok {
			tokens = append(tokens, tok)
			continue
		}
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

		// Открывающая скобка. Для "{" дополнительно проверяем, не тело ли
		// это ожидающей управляющей конструкции (if/while/for/loop/match) -
		// тогда увеличиваем текущую вложенность и, для match, запоминаем
		// глубину, на которой будут лежать его ветви. Параллельно решаем,
		// что это за "{" с точки зрения графа для метрики граничных
		// значений: тело функции, ветвь if/while/for, "else {...}" или
		// тело ветви match, оформленное блоком.
		if c == '(' || c == '{' || c == '[' {
			declish := false
			isControlBody := false
			var controlKind byte
			if c == '{' {
				l.pendingImpl = false
				if l.headerDepth == len(l.bracketStack) {
					l.headerDepth = -1
				}
				if l.pendingDeclHeader {
					declish = true
					l.pendingDeclHeader = false
				} else if n := len(l.bracketStack); n > 0 && l.bracketStack[n-1].isDeclBody {
					declish = true
				}
				if n := len(l.pendingControls); n > 0 && l.pendingControls[n-1].depth == len(l.bracketStack) {
					p := l.pendingControls[n-1]
					l.pendingControls = l.pendingControls[:n-1]
					isControlBody = true
					controlKind = p.kind
					l.currentNesting++
					if l.currentNesting > l.gilb.MaxNesting {
						l.gilb.MaxNesting = l.currentNesting
					}
				}
			}

			cfElseClose := false
			cfArmClose := false
			cfFnBody := false
			if c == '{' {
				if isControlBody && controlKind == 'o' && len(l.cfSelStack) > 0 {
					l.cfStack = append(l.cfStack, &cfBlockBuilder{})
				} else if !isControlBody && l.cfAwaitingElse != nil {
					l.cfStack = append(l.cfStack, &cfBlockBuilder{})
					l.cfElseBodyFor = append(l.cfElseBodyFor, l.cfAwaitingElse)
					l.cfAwaitingElse = nil
					cfElseClose = true
				} else if !isControlBody && l.cfAwaitingMatchArm {
					l.cfStack = append(l.cfStack, &cfBlockBuilder{})
					l.cfAwaitingMatchArm = false
					cfArmClose = true
				}
				if l.pendingFnBody {
					l.pendingFnBody = false
					l.cfStack = append(l.cfStack, &cfBlockBuilder{})
					cfFnBody = true
				}
			}

			l.bracketStack = append(l.bracketStack, bracketFrame{
				char: c, isDeclBody: declish, pure: c == '{',
				isControlBody: isControlBody, controlKind: controlKind,
				cfFnBody: cfFnBody, cfElseClose: cfElseClose, cfArmClose: cfArmClose,
			})
			if controlKind == 'm' {
				l.matchBodyDepths = append(l.matchBodyDepths, len(l.bracketStack))
			}
			l.pos++
			continue
		}

		// Закрывающая скобка. При выходе из тела управляющей конструкции
		// откатываем вложенность (и, для match, стек глубин его ветвей),
		// а также закрываем соответствующий блок для метрики граничных
		// значений: тело функции закрывает весь граф этой функции, тело
		// if/while/for присоединяется как очередная ветвь к своей вершине
		// выбора, тело match-ветви - как ветвь match, "else {...}" -
		// как вторая ветвь своего if.
		if c == ')' || c == '}' || c == ']' {
			l.pos++
			if n := len(l.bracketStack); n > 0 {
				frame := l.bracketStack[n-1]
				l.bracketStack = l.bracketStack[:n-1]
				if frame.isControlBody {
					l.currentNesting--
					if frame.controlKind == 'm' && len(l.matchBodyDepths) > 0 {
						l.matchBodyDepths = l.matchBodyDepths[:len(l.matchBodyDepths)-1]
					}
				}

				switch {
				case frame.cfFnBody:
					if n := len(l.cfStack); n > 0 {
						top := l.cfStack[n-1]
						top.flush()
						l.cfStack = l.cfStack[:n-1]
						nodes := append(top.nodes, &cfNode{kind: 'e'})
						l.cfRoots = append(l.cfRoots, nodes)
					}
				case frame.isControlBody && frame.controlKind == 'o':
					if n := len(l.cfStack); n > 0 && len(l.cfSelStack) > 0 {
						top := l.cfStack[n-1]
						top.flush()
						l.cfStack = l.cfStack[:n-1]
						bodyNodes := top.nodes

						sf := l.cfSelStack[len(l.cfSelStack)-1]
						l.cfSelStack = l.cfSelStack[:len(l.cfSelStack)-1]
						sf.node.branches = append(sf.node.branches, bodyNodes)

						if sf.ctorKind == 'w' {
							sf.node.branches = append(sf.node.branches, []*cfNode{})
							l.attachSelNode(sf.node)
						} else {
							p := l.skipSpacesLookahead()
							if l.wordAt(p, "else") {
								q := p + 4
								for q < l.n && isSpaceByte(l.src[q]) {
									q++
								}
								if l.wordAt(q, "if") {
									l.cfElseIfChain = append(l.cfElseIfChain, sf.node)
								} else {
									l.cfAwaitingElse = sf.node
								}
							} else {
								sf.node.branches = append(sf.node.branches, []*cfNode{})
								l.attachSelNode(sf.node)
							}
						}
					}
				case frame.controlKind == 'm':
					if n := len(l.cfMatchStack); n > 0 {
						mnode := l.cfMatchStack[n-1]
						l.cfMatchStack = l.cfMatchStack[:n-1]
						l.cfMatchDepth = l.cfMatchDepth[:len(l.cfMatchDepth)-1]
						l.attachSelNode(mnode)
					}
				case frame.cfElseClose:
					if n := len(l.cfStack); n > 0 && len(l.cfElseBodyFor) > 0 {
						top := l.cfStack[n-1]
						top.flush()
						l.cfStack = l.cfStack[:n-1]
						owner := l.cfElseBodyFor[len(l.cfElseBodyFor)-1]
						l.cfElseBodyFor = l.cfElseBodyFor[:len(l.cfElseBodyFor)-1]
						owner.branches = append(owner.branches, top.nodes)
						l.attachSelNode(owner)
					}
				case frame.cfArmClose:
					if n := len(l.cfStack); n > 0 && len(l.cfMatchStack) > 0 {
						top := l.cfStack[n-1]
						top.flush()
						l.cfStack = l.cfStack[:n-1]
						mnode := l.cfMatchStack[len(l.cfMatchStack)-1]
						mnode.branches = append(mnode.branches, top.nodes)
					}
				}

				if frame.callName != "" {
					opText := frame.callName + bracketPairText(frame.char)
					tokens = append(tokens, token{Text: opText, Operand: false})

					p := l.skipSpacesLookahead()
					stmtEnd := frame.stmtLevel && p < l.n && l.src[p] == ';'
					if !frame.isDecl && !stmtEnd && !l.isPatternEnd(p) {
						tokens = append(tokens, token{Text: opText, Operand: true})
					}
				} else {
					tokens = append(tokens, token{Text: bracketPairText(frame.char), Operand: false})
				}
			} else {
				tokens = append(tokens, token{Text: string(c), Operand: false})
			}
			continue
		}

		if c == ':' && l.peekAt(1) != ':' {
			depth := len(l.bracketStack)
			lastIsLifetimeLabel := len(tokens) > 0 && strings.HasPrefix(tokens[len(tokens)-1].Text, "'")
			isParamColon := depth > 0 && l.bracketStack[depth-1].char == '(' && l.bracketStack[depth-1].callName != ""
			isFieldColon := depth > 0 && l.bracketStack[depth-1].char == '{' && l.bracketStack[depth-1].isDeclBody
			isAnnotColon := l.pendingAnnot.active && l.pendingAnnot.baseDepth == depth
			if !lastIsLifetimeLabel && (isParamColon || isFieldColon || isAnnotColon) {
				l.pendingAnnot.active = false
				l.pos++
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
			l.pendingFnBody = false
			if l.headerDepth == len(l.bracketStack) {
				l.headerDepth = -1
			}
			if l.pendingAnnot.active && l.pendingAnnot.baseDepth == len(l.bracketStack) {
				l.pendingAnnot.active = false
			}
		case "=":
			tokens = append(tokens, tok)
			if l.pendingAnnot.active && l.pendingAnnot.baseDepth == len(l.bracketStack) {
				wasTypeAlias := l.pendingAnnot.isTypeAlias
				l.pendingAnnot.active = false
				if wasTypeAlias {
					l.skipTypeExpr(typeStop{chars: []byte{';'}})
				}
			}
		case "->":
			l.skipTypeExpr(typeStop{chars: []byte{'{', ';'}, words: []string{"where"}})
		case "=>":
			// Ветвь match: считаем в сложность Джилба, только если "=>"
			// стоит на глубине ветвей ближайшего открытого match, а не
			// внутри тела самой ветви, и паттерн не является заглушкой
			// "_". Для метрики граничных значений, напротив, учитываем
			// ЛЮБУЮ ветвь, включая "_" - для графа это реальная дуга.
			tokens = append(tokens, tok)
			atArmLevel := len(l.matchBodyDepths) > 0 && l.matchBodyDepths[len(l.matchBodyDepths)-1] == len(l.bracketStack)
			isWildcard := len(tokens) >= 2 && tokens[len(tokens)-2].Text == "_"
			if atArmLevel && !isWildcard {
				l.gilb.Absolute++
				l.gilb.Kinds["match"]++
			}
			if atArmLevel && len(l.cfMatchStack) > 0 {
				p := l.skipSpacesLookahead()
				if p < l.n && l.src[p] == '{' {
					l.cfAwaitingMatchArm = true
				} else {
					mnode := l.cfMatchStack[len(l.cfMatchStack)-1]
					mnode.branches = append(mnode.branches, []*cfNode{{kind: 'r'}})
				}
			}
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
	l.pos += 2
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

func (l *rustLexer) tryByteString() (token, bool) {
	if l.peek() != 'b' || l.peekAt(1) != '"' {
		return token{}, false
	}
	start := l.pos
	l.pos++
	l.advanceOverStringBody()
	return token{Text: string(l.src[start:l.pos]), Operand: true}, true
}

func (l *rustLexer) tryByteChar() (token, bool) {
	if l.peek() != 'b' || l.peekAt(1) != '\'' {
		return token{}, false
	}
	start := l.pos
	l.pos++
	l.advanceOverCharBody()
	return token{Text: string(l.src[start:l.pos]), Operand: true}, true
}

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
	l.pos++
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
	l.pos++
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
			return
		}
		l.pos++
	}
}

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
		if l.pos < l.n && l.src[l.pos] == '.' &&
			l.peekAt(1) != '.' && !isIdentStartByte(l.peekAt(1)) {
			l.pos++
			for l.pos < l.n && (isDigit(l.src[l.pos]) || l.src[l.pos] == '_') {
				l.pos++
			}
		}
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

	for l.pos < l.n {
		r, size := utf8.DecodeRune(l.src[l.pos:])
		if !isIdentContinue(r) {
			break
		}
		l.pos += size
	}

	return token{Text: string(l.src[start:l.pos]), Operand: true}
}

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

	if l.peek() == '!' && l.peekAt(1) != '=' {
		l.pos++
		macroName := text + "!"
		if p := l.skipSpacesLookahead(); p < l.n {
			switch l.src[p] {
			case '(', '[', '{':
				open := l.src[p]
				l.pos = p + 1
				l.bracketStack = append(l.bracketStack, bracketFrame{
					char:      open,
					callName:  macroName,
					pure:      open == '{',
					stmtLevel: l.curPure(),
				})
				return token{}, false
			}
		}
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
			l.headerDepth = len(l.bracketStack)
			if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '<' {
				l.pos = p
				l.skipGenerics()
			}
		case "if", "while":
			// Обычное ветвление/цикл: сразу считаем в AC и ставим "ожидание
			// тела" для подсчёта вложенности (Джилб) и для метрики
			// граничных значений заводим вершину выбора.
			l.headerDepth = len(l.bracketStack)
			l.pendingControls = append(l.pendingControls, controlPending{depth: len(l.bracketStack), kind: 'o'})
			l.gilb.Absolute++
			l.gilb.Kinds[text]++
			if n := len(l.cfStack); n > 0 {
				l.cfStack[n-1].flush()
				ck := byte('w')
				if text == "if" {
					ck = 'i'
				}
				l.cfSelStack = append(l.cfSelStack, &cfSelFrame{node: &cfNode{kind: 's'}, ctorKind: ck})
			}
		case "loop":
			l.pendingControls = append(l.pendingControls, controlPending{depth: len(l.bracketStack), kind: 'o'})
			l.gilb.Absolute++
			l.gilb.Kinds["loop"]++
			// Для метрики граничных значений loop сам по себе не ветвится -
			// ветвление создаёт только условный break (вложенный if),
			// поэтому отдельную вершину выбора для него не создаём.
		case "match":
			// Сам match в AC Джилба не добавляем - складываем число его
			// ветвей (см. обработку "=>"), но вложенность считаем как у
			// обычной управляющей конструкции. Для графа заводим вершину
			// выбора match.
			l.headerDepth = len(l.bracketStack)
			l.pendingControls = append(l.pendingControls, controlPending{depth: len(l.bracketStack), kind: 'm'})
			if n := len(l.cfStack); n > 0 {
				l.cfStack[n-1].flush()
				l.cfMatchStack = append(l.cfMatchStack, &cfNode{kind: 's'})
				l.cfMatchDepth = append(l.cfMatchDepth, n)
			}
		case "where":
			l.headerDepth = len(l.bracketStack)
		case "for":
			if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '<' {
				l.pos = p
				l.skipGenerics()
				return token{Text: "for<>", Operand: false}, true
			}
			if l.pendingImpl {
				l.pendingImpl = false
				return token{Text: "impl-for", Operand: false}, true
			}
			// обычный цикл for
			l.headerDepth = len(l.bracketStack)
			l.pendingControls = append(l.pendingControls, controlPending{depth: len(l.bracketStack), kind: 'o'})
			l.gilb.Absolute++
			l.gilb.Kinds["for"]++
			if n := len(l.cfStack); n > 0 {
				l.cfStack[n-1].flush()
				l.cfSelStack = append(l.cfSelStack, &cfSelFrame{node: &cfNode{kind: 's'}, ctorKind: 'w'})
			}
		case "fn":
			l.expectName()
			l.pendingFnBody = true
		case "trait":
			l.expectName()
			l.headerDepth = len(l.bracketStack)
		case "struct", "enum", "union":
			l.skipTypeDecl()
		case "let", "const", "static":
			l.pendingAnnot = pendingAnnotation{active: true, baseDepth: len(l.bracketStack)}
		case "type":
			l.pendingAnnot = pendingAnnotation{active: true, baseDepth: len(l.bracketStack), isTypeAlias: true}
			l.expectName()
		}
		return token{Text: text, Operand: false}, true
	}

	wasName := l.pendingName
	name := text
	if l.pendingName {
		l.pendingName = false
		if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '<' {
			l.pos = p
			l.skipGenerics()
		}
	}

	if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '{' &&
		l.headerDepth != len(l.bracketStack) {
		if r, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(r) {
			l.pos = p + 1

			declish := false
			if l.pendingDeclHeader {
				declish = true
				l.pendingDeclHeader = false
			} else if n := len(l.bracketStack); n > 0 && l.bracketStack[n-1].isDeclBody {
				declish = true
			}
			l.bracketStack = append(l.bracketStack, bracketFrame{
				char:       '{',
				callName:   name,
				isDeclBody: declish,
				pure:       true,
				stmtLevel:  l.curPure(),
				isDecl:     wasName || declish,
			})
			return token{}, false
		}
	}

	if p := l.skipSpacesLookahead(); p < l.n && l.src[p] == '(' {
		l.pos = p + 1

		declish := l.pendingDeclHeader
		if !declish {
			if n := len(l.bracketStack); n > 0 && l.bracketStack[n-1].isDeclBody {
				declish = true
			}
		}
		l.bracketStack = append(l.bracketStack, bracketFrame{
			char:       '(',
			callName:   name,
			isDeclBody: declish,
			stmtLevel:  l.curPure(),
			isDecl:     wasName || declish,
		})
		if declish {
			l.pendingDeclHeader = false
			l.skipTypeExpr(typeStop{chars: []byte{')'}})
		}
		return token{}, false
	}

	return token{Text: name, Operand: true}, true
}

var threeCharOps = []string{"<<=", ">>=", "..=", "..."}

var twoCharOps = []string{
	"::", "->", "=>", "==", "!=", "<=", ">=", "&&", "||",
	"+=", "-=", "*=", "/=", "%=", "^=", "&=", "|=", "<<", ">>", "..",
}

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

var rustKeywords = map[string]bool{
	"as": true, "async": true, "await": true, "break": true,
	"const": true, "continue": true, "crate": true, "dyn": true,
	"else": true, "enum": true, "extern": true, "fn": true, "for": true,
	"if": true, "impl": true, "in": true, "let": true, "loop": true,
	"match": true, "mod": true, "move": true, "mut": true, "pub": true,
	"ref": true, "return": true, "static": true, "struct": true,
	"super": true, "trait": true, "type": true, "unsafe": true,
	"use": true, "where": true, "while": true,
	"abstract": true, "become": true, "box": true, "do": true,
	"final": true, "macro": true, "override": true, "priv": true,
	"typeof": true, "unsized": true, "virtual": true, "yield": true,
	"try": true, "union": true,
}

var referenceOperators = map[string]bool{
	// арифметические и побитовые (бинарные), они же унарные - и *
	"+": true, "-": true, "*": true, "/": true, "%": true,
	"^": true, "&": true, "|": true, "<<": true, ">>": true,
	// сравнение
	"==": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true,
	// логические (ленивые)
	"&&": true, "||": true,
	// унарное отрицание
	"!": true,
	// присваивание и составное присваивание
	"=": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true,
	"^=": true, "&=": true, "|=": true, "<<=": true, ">>=": true,
	// диапазоны
	"..": true, "..=": true,
	// оператор "?"
	"?": true,
	// приведение типа
	"as": true,
}