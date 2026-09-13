package main

import (
	"fmt"
	"sort"

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

// AppGUI инкапсулирует в себе все элементы управления интерфейсом
type AppGUI struct {
	window        fyne.Window
	lblFile       *widget.Label
	lblN1         *widget.Label
	lblN2         *widget.Label
	lblTotalN1    *widget.Label
	lblTotalN2    *widget.Label
	lblVocab      *widget.Label
	lblLen        *widget.Label
	lblVol        *widget.Label
	opRows        []TableRow
	valRows       []TableRow
	listOperators *widget.List
	listOperands  *widget.List
}

func NewAppGUI(w fyne.Window) *AppGUI {
	gui := &AppGUI{window: w}
	gui.initUI()
	return gui
}

func (g *AppGUI) initUI() {
	g.lblFile = widget.NewLabel("Файл не выбран")
	g.lblFile.TextStyle = fyne.TextStyle{Italic: true}

	g.lblN1 = widget.NewLabel("η1 (Словарь операторов): -")
	g.lblN2 = widget.NewLabel("η2 (Словарь операндов): -")
	g.lblTotalN1 = widget.NewLabel("N1 (Всего операторов): -")
	g.lblTotalN2 = widget.NewLabel("N2 (Всего операндов): -")
	g.lblVocab = widget.NewLabel("η (Словарь программы): -")
	g.lblLen = widget.NewLabel("N (Длина программы): -")
	g.lblVol = widget.NewLabel("V (Объем программы): -")

	// Инициализация списков-таблиц
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

	// Кнопка выбора файла
	btnOpen := widget.NewButton("Открыть Rust файл", g.handleOpenFile)

	// Компоновка окон (Layout)
	topContainer := container.NewVBox(btnOpen, g.lblFile)
	metricsContainer := container.NewVBox(
		widget.NewLabelWithStyle("Итоговые результаты:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		g.lblN1, g.lblN2, g.lblTotalN1, g.lblTotalN2,
		widget.NewSeparator(),
		g.lblVocab, g.lblLen, g.lblVol,
	)
	tablesContainer := container.NewGridWithColumns(2,
		container.NewBorder(widget.NewLabelWithStyle("Операторы", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}), nil, nil, nil, g.listOperators),
		container.NewBorder(widget.NewLabelWithStyle("Операнды", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}), nil, nil, nil, g.listOperands),
	)

	mainLayout := container.NewBorder(topContainer, nil, metricsContainer, nil, tablesContainer)
	g.window.SetContent(mainLayout)
}

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

		g.lblFile.SetText("Анализ: " + reader.URI().Name())

		// Вызываем бизнес-логику из analyzer.go
		metrics, err := AnalyzeRustFile(reader.URI().Path())
		if err != nil {
			dialog.ShowError(err, g.window)
			return
		}

		// Обновляем текстовые поля интерфейса данными из структуры
		g.lblN1.SetText(fmt.Sprintf("η1 (Словарь операторов): %.0f", metrics.N1))
		g.lblN2.SetText(fmt.Sprintf("η2 (Словарь операндов): %.0f", metrics.N2))
		g.lblTotalN1.SetText(fmt.Sprintf("N1 (Всего операторов): %.0f", metrics.TotalN1))
		g.lblTotalN2.SetText(fmt.Sprintf("N2 (Всего операндов): %.0f", metrics.TotalN2))
		g.lblVocab.SetText(fmt.Sprintf("η (Словарь программы): %.0f + %.0f = %.0f", metrics.N1, metrics.N2, metrics.Vocabulary))
		g.lblLen.SetText(fmt.Sprintf("N (Длина программы): %.0f + %.0f = %.0f", metrics.TotalN1, metrics.TotalN2, metrics.Length))
		g.lblVol.SetText(fmt.Sprintf("V (Объем программы): %.0f * log2(%.0f) = %.0f бит", metrics.Length, metrics.Vocabulary, metrics.Volume))

		// Перестраиваем списки операторов и операндов для таблиц
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

		// Обновляем списки на экране
		g.listOperators.Refresh()
		g.listOperands.Refresh()

	}, g.window)

	fd.SetFilter(storage.NewExtensionFileFilter([]string{".rs"}))
	fd.Show()
}
