package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("Анализатор метрик Холстеда (Rust)")
	myWindow.Resize(fyne.NewSize(750, 550))

	NewAppGUI(myWindow)

	myWindow.ShowAndRun()
}
