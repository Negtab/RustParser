package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

type TableRow struct {
	Key   string
	Count string
}

type AppGUI struct {
	window fyne.Window

	lblFile   *widget.Label
	lblSource *widget.Label

	// Метрики Холстеда
	lblN1      *widget.Label
	lblN2      *widget.Label
	lblTotalN1 *widget.Label
	lblTotalN2 *widget.Label
	lblVocab   *widget.Label
	lblLen     *widget.Label
	lblVol     *widget.Label

	opRows        []TableRow
	valRows       []TableRow
	listOperators *widget.List
	listOperands  *widget.List

	// Метрики Джилба
	lblGilbAbs   *widget.Label
	lblGilbRel   *widget.Label
	lblGilbCount *widget.Label
	lblGilbNest  *widget.Label

	gilbOpRows  []TableRow
	listGilbOps *widget.List

	// Метрика граничных значений
	lblBoundarySa *widget.Label
	lblBoundarySo *widget.Label
	lblBoundaryV  *widget.Label

	// metricsArea переключается между placeholder/halsteadView/gilbView/
	// boundaryView - одновременно показывается только один из наборов.
	metricsArea  *fyne.Container
	placeholder  fyne.CanvasObject
	halsteadView fyne.CanvasObject
	gilbView     fyne.CanvasObject
	boundaryView fyne.CanvasObject

	currentFilePath string
	metrics         *StructuralMetrics // кэш результата анализа текущего файла
}

func NewAppGUI(w fyne.Window) *AppGUI {
	gui := &AppGUI{window: w}
	gui.initUI()
	return gui
}

func (g *AppGUI) initUI() {
	g.lblFile = widget.NewLabel("Файл не выбран")
	g.lblFile.TextStyle = fyne.TextStyle{Italic: true}

	g.lblSource = widget.NewLabel("")
	g.lblSource.TextStyle = fyne.TextStyle{Monospace: true}
	sourceScroll := container.NewScroll(g.lblSource)
	sourcePanel := container.NewBorder(
		widget.NewLabelWithStyle("Исходный код", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		nil, nil, nil,
		sourceScroll,
	)

	g.buildHalsteadView()
	g.buildGilbView()
	g.buildBoundaryView()
	g.placeholder = widget.NewLabel("Откройте файл и выберите метрику для расчёта.")

	g.metricsArea = container.NewStack(g.placeholder)

	btnOpen := widget.NewButton("Открыть файл", g.handleOpenFile)
	btnHalstead := widget.NewButton("Посчитать метрики Холстеда", g.handleComputeHalstead)
	btnGilb := widget.NewButton("Посчитать метрики Джилба", g.handleComputeGilb)
	btnBoundary := widget.NewButton("Посчитать метрику граничных значений", g.handleComputeBoundary)

	topContainer := container.NewVBox(
		container.NewHBox(btnOpen, btnHalstead, btnGilb, btnBoundary),
		g.lblFile,
	)

	split := container.NewHSplit(sourcePanel, g.metricsArea)
	split.Offset = 0.4

	mainLayout := container.NewBorder(topContainer, nil, nil, nil, split)
	g.window.SetContent(mainLayout)
}

// buildHalsteadView собирает панель с метриками Холстеда и таблицами
// операторов/операндов. Ничего, относящегося к Джилбу или граничным
// значениям, здесь нет.
func (g *AppGUI) buildHalsteadView() {
	g.lblN1 = widget.NewLabel("η1 (Словарь операторов): -")
	g.lblN2 = widget.NewLabel("η2 (Словарь операндов): -")
	g.lblTotalN1 = widget.NewLabel("N1 (Всего операторов): -")
	g.lblTotalN2 = widget.NewLabel("N2 (Всего операндов): -")
	g.lblVocab = widget.NewLabel("η (Словарь программы): -")
	g.lblLen = widget.NewLabel("N (Длина программы): -")
	g.lblVol = widget.NewLabel("V (Объем программы): -")

	metricsBox := container.NewVBox(
		widget.NewLabelWithStyle("Метрики Холстеда:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		g.lblN1, g.lblN2, g.lblTotalN1, g.lblTotalN2,
		widget.NewSeparator(),
		g.lblVocab, g.lblLen, g.lblVol,
	)

	g.listOperators = widget.NewList(
		func() int { return len(g.opRows) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(g.opRows) {
				o.(*widget.Label).SetText(fmt.Sprintf("%s : %s", g.opRows[i].Key, g.opRows[i].Count))
			}
		},
	)
	g.listOperands = widget.NewList(
		func() int { return len(g.valRows) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(g.valRows) {
				o.(*widget.Label).SetText(fmt.Sprintf("%s : %s", g.valRows[i].Key, g.valRows[i].Count))
			}
		},
	)

	tablesContainer := container.NewGridWithColumns(2,
		container.NewBorder(widget.NewLabelWithStyle("Операторы", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}), nil, nil, nil, g.listOperators),
		container.NewBorder(widget.NewLabelWithStyle("Операнды", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}), nil, nil, nil, g.listOperands),
	)

	g.halsteadView = container.NewBorder(metricsBox, nil, nil, nil, tablesContainer)
}

// buildGilbView собирает панель с метриками Джилба и списком операторов,
// учтённых в знаменателе OC (узкий список по Rust Reference). Ничего,
// относящегося к Холстеду или граничным значениям, здесь нет.
func (g *AppGUI) buildGilbView() {
	g.lblGilbAbs = widget.NewLabel("AC (Абсолютная сложность): -")
	g.lblGilbRel = widget.NewLabel("OC (Относительная сложность): -")
	g.lblGilbCount = widget.NewLabel("Количество операторов: -")
	g.lblGilbNest = widget.NewLabel("Максимальный уровень вложенности: -")

	metricsBox := container.NewVBox(
		widget.NewLabelWithStyle("Метрики Джилба:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		g.lblGilbAbs, g.lblGilbRel, g.lblGilbCount, g.lblGilbNest,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Операторы, учтённые в OC:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)

	g.listGilbOps = widget.NewList(
		func() int { return len(g.gilbOpRows) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(g.gilbOpRows) {
				o.(*widget.Label).SetText(fmt.Sprintf("%s : %s", g.gilbOpRows[i].Key, g.gilbOpRows[i].Count))
			}
		},
	)

	g.gilbView = container.NewBorder(metricsBox, nil, nil, nil, g.listGilbOps)
}

// buildBoundaryView собирает панель с метрикой граничных значений.
// Ничего, относящегося к Холстеду или Джилбу, здесь нет.
func (g *AppGUI) buildBoundaryView() {
	g.lblBoundarySa = widget.NewLabel("Sa (Абсолютная граничная сложность): -")
	g.lblBoundarySo = widget.NewLabel("So (Относительная граничная сложность): -")
	g.lblBoundaryV = widget.NewLabel("ν (Общее число вершин графа): -")

	g.boundaryView = container.NewVBox(
		widget.NewLabelWithStyle("Метрика граничных значений:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		g.lblBoundarySa, g.lblBoundarySo, g.lblBoundaryV,
	)
}

// showView переключает правую панель на один из видов; остальные в этот
// момент не показываются.
func (g *AppGUI) showView(view fyne.CanvasObject) {
	g.metricsArea.Objects = []fyne.CanvasObject{view}
	g.metricsArea.Refresh()
}

// handleOpenFile только загружает файл и показывает исходный код.
// Никакие метрики при этом не считаются и не выводятся.
func (g *AppGUI) handleOpenFile() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		defer func(reader fyne.URIReadCloser) {
			err := reader.Close()
			if err != nil {

			}
		}(reader)

		data, err := io.ReadAll(reader)
		if err != nil {
			dialog.ShowError(err, g.window)
			return
		}

		g.currentFilePath = reader.URI().Path()
		g.metrics = nil // сбрасываем кэш - файл новый, метрики нужно посчитать заново

		g.lblFile.SetText("Файл: " + reader.URI().Name())
		g.lblSource.SetText(strings.ReplaceAll(string(data), "\r\n", "\n"))

		g.showView(g.placeholder)
	}, g.window)

	fd.SetFilter(storage.NewExtensionFileFilter([]string{".rs"}))
	fd.Show()
}

// ensureMetrics считает метрики для текущего файла один раз и кэширует
// результат, чтобы все кнопки могли им пользоваться без повторного анализа.
func (g *AppGUI) ensureMetrics() (*StructuralMetrics, bool) {
	if g.currentFilePath == "" {
		dialog.ShowInformation("Нет файла", "Сначала откройте Rust файл.", g.window)
		return nil, false
	}
	if g.metrics != nil {
		return g.metrics, true
	}
	metrics, err := AnalyzeRustFile(g.currentFilePath)
	if err != nil {
		dialog.ShowError(err, g.window)
		return nil, false
	}
	g.metrics = metrics
	return metrics, true
}

// handleComputeHalstead показывает только метрики Холстеда.
func (g *AppGUI) handleComputeHalstead() {
	metrics, ok := g.ensureMetrics()
	if !ok {
		return
	}

	g.lblN1.SetText(fmt.Sprintf("η1 (Словарь операторов): %.0f", metrics.N1))
	g.lblN2.SetText(fmt.Sprintf("η2 (Словарь операндов): %.0f", metrics.N2))
	g.lblTotalN1.SetText(fmt.Sprintf("N1 (Всего операторов): %.0f", metrics.TotalN1))
	g.lblTotalN2.SetText(fmt.Sprintf("N2 (Всего операндов): %.0f", metrics.TotalN2))
	g.lblVocab.SetText(fmt.Sprintf("η (Словарь программы): %.0f + %.0f = %.0f", metrics.N1, metrics.N2, metrics.Vocabulary))
	g.lblLen.SetText(fmt.Sprintf("N (Длина программы): %.0f + %.0f = %.0f", metrics.TotalN1, metrics.TotalN2, metrics.Length))
	g.lblVol.SetText(fmt.Sprintf("V (Объем программы): %.0f * log2(%.0f) = %.0f бит", metrics.Length, metrics.Vocabulary, metrics.Volume))

	g.opRows = nil
	for k, v := range metrics.Operators {
		g.opRows = append(g.opRows, TableRow{Key: k, Count: fmt.Sprintf("%d", v)})
	}
	sort.Slice(g.opRows, func(i, j int) bool { return g.opRows[i].Key < g.opRows[j].Key })

	g.valRows = nil
	for k, v := range metrics.Operands {
		g.valRows = append(g.valRows, TableRow{Key: k, Count: fmt.Sprintf("%d", v)})
	}
	sort.Slice(g.valRows, func(i, j int) bool { return g.valRows[i].Key < g.valRows[j].Key })

	g.listOperators.Refresh()
	g.listOperands.Refresh()

	g.showView(g.halsteadView)
}

// handleComputeGilb показывает только метрики Джилба.
func (g *AppGUI) handleComputeGilb() {
	metrics, ok := g.ensureMetrics()
	if !ok {
		return
	}

	m := metrics.Gilb
	if m.MaxNesting != 0 {
		m.MaxNesting--
	}

	g.lblGilbAbs.SetText(fmt.Sprintf("AC (Абсолютная сложность): %d", m.Absolute))
	g.lblGilbRel.SetText(fmt.Sprintf("OC (Относительная сложность): %.3f", m.Relative))
	g.lblGilbCount.SetText(fmt.Sprintf("Количество операторов: %d", m.Count))
	g.lblGilbNest.SetText(fmt.Sprintf("Максимальный уровень вложенности: %d", m.MaxNesting))

	g.gilbOpRows = nil
	for k, v := range metrics.Operators {
		if referenceOperators[k] {
			g.gilbOpRows = append(g.gilbOpRows, TableRow{Key: k, Count: fmt.Sprintf("%d", v)})
		}
	}
	sort.Slice(g.gilbOpRows, func(i, j int) bool { return g.gilbOpRows[i].Key < g.gilbOpRows[j].Key })

	for _, kind := range []string{"if", "for", "while", "loop", "match"} {
		if v, ok := m.Kinds[kind]; ok {
			g.gilbOpRows = append(g.gilbOpRows, TableRow{Key: kind, Count: fmt.Sprintf("%d", v)})
		}
	}

	g.listGilbOps.Refresh()

	g.showView(g.gilbView)
}

// handleComputeBoundary показывает только метрику граничных значений.
func (g *AppGUI) handleComputeBoundary() {
	metrics, ok := g.ensureMetrics()
	if !ok {
		return
	}

	m := metrics.Boundary
	g.lblBoundarySa.SetText(fmt.Sprintf("Sa (Абсолютная граничная сложность): %.0f", m.Absolute))
	g.lblBoundarySo.SetText(fmt.Sprintf("So (Относительная граничная сложность): %.3f", m.Relative))
	g.lblBoundaryV.SetText(fmt.Sprintf("ν (Общее число вершин графа): %d", m.Vertices))

	g.showView(g.boundaryView)
}