package main

import (
	"flag"
	"fmt"
	"image"
	"image/draw"
	"io/ioutil"
	"log"
	"time"

	forecast "github.com/mlbright/forecast/v2"
	"github.com/golang/freetype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/font"
	"periph.io/x/periph/conn/gpio"
	"periph.io/x/periph/conn/gpio/gpioreg"
	"periph.io/x/periph/conn/spi/spireg"
	"periph.io/x/periph/devices/ssd1306"
	"periph.io/x/periph/host"
)

type fontbytes struct {
	File string
	B []byte
}

type fonts struct {
	Fonts map[string]*fontbytes
}

type state struct {
	Time string
	Date string
	Weather *forecast.Forecast
	News []string
}

type textLine struct {
	Text string
	Size float64
	Font string
}

type imageContents struct {
	Lines []*textLine
}

// Global Vars for config
var weatherkey  = flag.String("weatherkey", "", "API Key for Dark Skies Weather")
var newskey     = flag.String("newskey", "", "API Key for Google News API")
var f *fonts

func checkErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func generateTimeAndDate(tchan chan string, dchan chan string) {
	// Generate time, and send down tchan
	ticker := time.NewTicker(time.Millisecond * 100)
	tstring := "Mon Jan 2 2006"
	for t := range ticker.C {
		tchan <- t.Format("15:04:05")
		if t.Format("Mon Jan 2 2006") != tstring {
			tstring = t.Format("Mon Jan 2 2006")
			dchan <- tstring
		}
	}
}

func checkWeather(key *string, lat string, long string, wchan chan *forecast.Forecast) {
	// Check the weather, and send down wchan
	f, err := forecast.Get(*key, lat, long, "now", forecast.UK, forecast.English)
	if err != nil {
		log.Println(err)
	} else {
		wchan <- f
	}
	ticker := time.NewTicker(time.Minute * 10)
	for _ = range ticker.C {
		f, err = forecast.Get(*key, lat, long, "now", forecast.UK, forecast.English)
		if err != nil {
			log.Println(err)
		} else {
			wchan <- f
		}
	}
}

func checkNews(key *string, nchan chan []string) {
	// Check news, and send down nchan
}

func coordinator(tchan chan string, dchan chan string, wchan chan *forecast.Forecast, nchan chan []string, statechan chan *state) {
	// coordinate shit
	s := &state{Time: "New Time", Date: "New Date"}
	for {
		select {
		case d := <-dchan:
			// date
			s.Date = d
		case w := <-wchan:
			// weather
			s.Weather = w
		case n := <-nchan:
			// news
			s.News = n
		case t := <-tchan:
			// time
			s.Time = t
			statechan <- s
		}
	}
}

func mode(mchan chan bool, pin string) {
	// Monitor gpio pin, and send bool to state machine
	p := gpioreg.ByName(pin)
	if p == nil {
		log.Fatal("Failed to find mode GPIO pin")
	}
	if err := p.In(gpio.PullUp, gpio.BothEdges); err != nil {
		log.Fatal(err)
	}
	for {
		p.WaitForEdge(-1)
		if !p.Read() {
			// Button is pressed, switch mode
			mchan <- true
		}
	}
}

func stateBuilder(statechan chan *state, icchan chan *imageContents) {
	// Receive states, generate imageContents and send to imageGenerator
	dateLine        := &textLine{Text: "New Date", Size: 14, Font: "mt"}
	weatherLine     := &textLine{Text: "New Weather", Size: 12, Font: "mt"}
	temperatureLine := &textLine{Text: "New Temp", Size: 14, Font: "mt"}
	timeLine        := &textLine{Text: "New Time", Size: 26, Font: "phage"}
	for {
		s := <-statechan
		// New State
		dateLine.Text         = s.Date
		if s.Weather != nil {
			weatherLine.Text      = s.Weather.Currently.Summary
			//temperatureLine.Text  = fmt.Sprintf("%.2f° Celsius", s.Weather.Currently.Temperature)
			tempc := s.Weather.Currently.Temperature
			tempf := float64(1.8 * tempc) + 32
			temperatureLine.Text  = fmt.Sprintf("%.1f°F %.1f°C", tempf, tempc)
		}
		timeLine.Text         = s.Time
		ic := &imageContents{Lines: []*textLine{dateLine, weatherLine, temperatureLine, timeLine}}
		icchan <- ic
	}
}

func modeStateMachine(icchan chan *imageContents, mchan chan bool, imgchan chan *image.RGBA) {
	// Receive imageContents, generate an RGBA and send to SPI device
	modes := []string{"default", "weather", "headlines", "time"}
	mindex := 0
	mode := modes[mindex]
	for {
		select {
		case <-mchan:
			mindex += 1
			if mindex > len(modes) -1 {
				mindex = 0
			}
			mode = modes[mindex]
		case ic := <-icchan:
			imgchan <-generateImage(ic, mode)
		}
	}
}

func drawText(fontkey string, size float64, text string, pt *fixed.Point26_6, img *image.RGBA) {
	// Read the font data.
	fg := image.White
	pf, err := freetype.ParseFont(f.Fonts[fontkey].B)
	checkErr(err)
	// Set up context
	c := freetype.NewContext()
	// Set the Y position
	pt.Y += c.PointToFixed(size/1.2)
	// 59 DPI screen
	c.SetDPI(72)
	c.SetFont(pf)
	c.SetFontSize(size)
	c.SetClip(img.Bounds())
	c.SetDst(img)
	c.SetSrc(fg)
	c.SetHinting(font.HintingNone)
	// Draw the text.
	_, err = c.DrawString(text, *pt)
	checkErr(err)
	if size < 20 {
		pt.Y += c.PointToFixed(2)
	}
}

func generateImage(ic *imageContents, mode string) (rgba *image.RGBA) {
	// Generate Image
	bg := image.Black
	rgba = image.NewRGBA(image.Rect(0, 0, 128, 64))
	draw.Draw(rgba, rgba.Bounds(), bg, image.ZP, draw.Src)
	pt := freetype.Pt(0, 0)
	switch mode {
	case "weather":
		// Do some special weather shit
	case "headlines":
		// New Headlines
	case "time":
		// Time bullshit
	default:
		// Default
		for _, tl := range ic.Lines {
			drawText(tl.Font, tl.Size, tl.Text, &pt, rgba)
		}
	}
	return rgba
}

func spiHandler(spiPort string, gpioPin string, imgchan chan *image.RGBA) {
	// Connect to SPI device, receive RGBA images, and update SPI device

	// Open a handle to the first available spi bus:
	bus, err := spireg.Open(spiPort)
	checkErr(err)

	var dc gpio.PinOut
	dc = gpioreg.ByName(gpioPin)

	// Open a handle to a ssd1306 OLED connected on the spi bus:
	dev, err := ssd1306.NewSPI(bus, dc, 128, 64, false)
	checkErr(err)

	for {
		rgba := <-imgchan
		dev.Draw(rgba.Bounds(), rgba, image.Point{})
	}
}

func setupFonts() {
	mt := &fontbytes{File: "/usr/local/share/fonts/MonospaceTypewriter.ttf"}
	phage := &fontbytes{File: "/usr/local/share/fonts/phage.ttf"}
        fb, err := ioutil.ReadFile(mt.File)
	checkErr(err)
        mt.B = fb
	fb, err = ioutil.ReadFile(phage.File)
	checkErr(err)
	phage.B = fb
	fnts := map[string]*fontbytes{
			"mt": mt,
			"phage": phage,
		}
	f = &fonts{Fonts: fnts}
}

func main() {
	// Load all the drivers:
	_, err := host.Init()
	checkErr(err)

	// reset screen
	reset := gpioreg.ByName("24")
	if reset == nil { log.Fatal("Failed to get GPIO24") }
	checkErr(reset.Out(gpio.Low))
	time.Sleep(500 * time.Millisecond)
	checkErr(reset.Out(gpio.High))

	// Create a shitload of channels
	tchan := make(chan string)
	dchan := make(chan string)
	wchan := make(chan *forecast.Forecast)
	nchan := make(chan []string)
	mchan := make(chan bool)
	statechan := make(chan *state)
	icchan := make(chan *imageContents)
	imgchan := make(chan *image.RGBA)

	//setup
	flag.Parse()
	setupFonts()

	// Start up goroutines
	go spiHandler("", "23", imgchan)
	go modeStateMachine(icchan, mchan, imgchan)
	go stateBuilder(statechan, icchan)
	go mode(mchan, "12")
	go coordinator(tchan, dchan, wchan, nchan, statechan)
	go checkNews(newskey, nchan)
	go checkWeather(weatherkey, "47.5566", "-122.3760", wchan)

	// Start generating timer ticks
	generateTimeAndDate(tchan, dchan)
}
