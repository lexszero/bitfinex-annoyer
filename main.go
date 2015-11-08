package main

import (
	"encoding/json"
	"fmt"
	"github.com/lexszero/bitfinex-api-go"
	. "github.com/mpatraw/gocurse/curses"
	. "github.com/mpatraw/gocurse/panels"
	"io/ioutil"
	"log"
	"math"
	"sort"
	"time"
)

type Config struct {
	ApiKey                 string
	ApiSecret              string
	Pair                   string
	OrderBookPrecision     string
	OrderBookLen           int
	PositionsLen           int
	OrdersLen              int
	HighlightTradesOver    float64
	HighlightOrderBookOver float64
	HistoryRecordPeriod    time.Duration
	HistoryHeight          int
}

type HistoryRecord struct {
	Timestamp             time.Time
	BuyAmount, SellAmount float64
}

var (
	conf Config

	winTicker, winTrades, winBookBid, winBookAsk, winHistory *WinPanel

	tablePositions, tableOrders *Table

	lastTrades []bitfinex.WebsocketTrade
	bookBid    = make(map[float64]*bitfinex.WebsocketBook)
	bookAsk    = make(map[float64]*bitfinex.WebsocketBook)
	positions  = make(map[string]bitfinex.WebsocketPosition)
	orders     = make(map[int64]bitfinex.WebsocketOrder)

	history        []HistoryRecord
	hist           *HistoryRecord
	historyUpdated time.Time

	bookBidSorted, bookAskSorted OrderBook
)

const (
	_ = iota
	clRed
	clGreen
	clBlue

	bookWidth   = 31
	tradesWidth = 25
	screenWidth = 87
)

type WinPanel struct {
	*Window
	*Panel
}

func NewWinPanel(height, width, y, x int, box bool, title string) (w *WinPanel) {
	w = &WinPanel{}
	var err error
	w.Window, err = Newwin(height, width, y, x)
	if err != nil {
		panic(err)
	}
	w.Panel = NewPanel(w.Window)
	if box {
		w.Attron(A_DIM)
		w.Box(0, 0)
		w.Attroff(A_DIM)
	}
	if title != "" {
		w.Addstr(0, 0, title, A_BOLD)
	}
	return w
}

type OrderBook []*bitfinex.WebsocketBook

func (b OrderBook) Len() int      { return len(b) }
func (b OrderBook) Swap(i, j int) { b[i], b[j] = b[j], b[i] }

type ByPrice struct{ OrderBook }

func (b ByPrice) Less(i, j int) bool {
	if b.OrderBook[i].Amount < 0 {
		return b.OrderBook[i].Price < b.OrderBook[j].Price
	} else {
		return b.OrderBook[j].Price < b.OrderBook[i].Price
	}
}

func updatePositions() {
	for _, v := range positions {
		var book *OrderBook
		if v.Amount < 0 {
			book = &bookAskSorted
		} else {
			book = &bookBidSorted
		}

		unclosed := v.Amount
		baseValue := v.Amount * v.Price
		value := 0.0
		for _, b := range *book {
			d := 0.0
			if math.Abs(b.Amount) < math.Abs(unclosed) {
				d = b.Amount
			} else {
				d = unclosed
			}
			value += d * b.Price
			unclosed -= d
			if math.Abs(unclosed) < 0.00001 {
				break
			}
		}
		profit := value - baseValue

		attrPL := int32(A_BOLD)
		if profit < 0 {
			attrPL |= Color_pair(clRed)
		} else {
			attrPL |= Color_pair(clGreen)
		}

		tablePositions.SetRowColAttr(v.Pair, 3, attrPL)
		tablePositions.SetRowColAttr(v.Pair, 4, attrPL)
		tablePositions.SetRowColAttr(v.Pair, 5, attrPL)
		tablePositions.UpdateRowValues(v.Pair, v.Status, v.Amount, v.Price,
			value/v.Amount, profit, (profit/math.Abs(baseValue))*100)
	}
}

func updateHistoryBar(n, baseline int, h float64, attr int32) {
	inc := int(math.Abs(h) / h)
	for i := 0; i != int(h); i += inc {
		winHistory.Addch(n, baseline-i, '*', attr)
	}
	if math.Abs(h) > float64(int(math.Abs(h))) {
		winHistory.Addch(n, baseline-(int(h)), '|', attr)
	}
}

func updateHistoryInfo() {
	winHistory.Addstr(0, 0, fmt.Sprintf("Last %d sec: buy %-6.2f, sell %-6.2f",
		conf.HistoryRecordPeriod, hist.BuyAmount, hist.SellAmount), 0)
}

func updateHistory() {
	if time.Since(historyUpdated) < time.Second {
		updateHistoryInfo()
		return
	}
	baseline := conf.HistoryHeight / 2
	var peak float64
	for _, v := range history {
		t := math.Max(v.BuyAmount, -v.SellAmount)
		peak = math.Max(t, peak)
	}
	scale := peak / float64(baseline)
	winHistory.Clear()
	for n, v := range history {
		updateHistoryBar(n, baseline-1, v.BuyAmount/scale, Color_pair(clGreen))
		updateHistoryBar(n, baseline, v.SellAmount/scale, Color_pair(clRed))
	}
	updateHistoryInfo()
	historyUpdated = time.Now()
}

func main() {
	buf, err := ioutil.ReadFile("config.json")
	if err != nil {
		log.Fatal("Unable to read config file:", err)
	}
	err = json.Unmarshal(buf, &conf)
	if err != nil {
		log.Fatal("Unable to parse config:", err)
	}

	_, err = Initscr()
	if err != nil {
		log.Fatal("Unable to initialize ncurses:", err)
	}
	defer Endwin()
	Start_color()
	Init_pair(clRed, COLOR_RED, COLOR_BLACK)
	Init_pair(clGreen, COLOR_GREEN, COLOR_BLACK)
	Init_pair(clBlue, COLOR_BLUE, COLOR_BLACK)
	x, y, w, h := 0, 0, 0, 0

	w, h = screenWidth, 1
	winTicker = NewWinPanel(h, w, x, y, false, "Ticker")
	x += h

	h = conf.OrderBookLen + 2
	w = bookWidth
	winBookBid = NewWinPanel(h, w, x, y, true, "Bid")
	y += w

	winBookAsk = NewWinPanel(h, w, x, y, true, "Ask")
	y += w

	w = tradesWidth
	winTrades = NewWinPanel(h, w, x, y, true, "Last trades")
	x += h
	w = y + w

	y = 0
	h = conf.HistoryHeight
	winHistory = NewWinPanel(h, w, x, y, false, "")
	x += h

	h = conf.PositionsLen + 3
	tablePositions = NewTable(h, w, x, y, "Positions", []*TableField{
		{"Status       ", -7, "s", 0, ""},
		{"Amount", -6, ".2f", A_BOLD, ""},
		{"Base price", -10, ".2f", A_BOLD, ""},
		{"Curr.price", -8, ".2f", A_BOLD, ""},
		{"P/L", -9, ".2f", A_BOLD, ""},
		{"P/L %", -6, ".2f", A_BOLD, ""},
	})
	x += h

	h = conf.OrdersLen + 3
	tableOrders = NewTable(h, w, x, y, "Orders", []*TableField{
		{"Type", -8, "s", 0, ""},
		{"Orig.Amount", -6, ".2f", A_BOLD, ""},
		{"Amount", -6, ".2f", A_BOLD, ""},
		{"Price", -8, ".2f", A_BOLD, ""},
		{"Avg.Price", -8, ".2f", A_BOLD, ""},
	})

	UpdatePanels()
	DoUpdate()

	lastTrades = make([]bitfinex.WebsocketTrade, conf.OrderBookLen)
	history = make([]HistoryRecord, screenWidth, screenWidth)
	hist = &history[len(history)-1]

	api := bitfinex.NewClient().Auth(conf.ApiKey, conf.ApiSecret)

	trades := make(chan bitfinex.WebsocketTrade)
	ticker := make(chan bitfinex.WebsocketTicker)
	book := make(chan bitfinex.WebsocketBook)
	account := make(chan bitfinex.WebsocketTerm)

	err = api.WebSocket.Connect()
	if err != nil {
		log.Fatal("Error connecting to WebSocket:", err)
	}
	defer api.WebSocket.Close()

	api.WebSocket.SubscribeTrades(conf.Pair, trades)
	api.WebSocket.SubscribeTicker(conf.Pair, ticker)
	api.WebSocket.SubscribeBook(conf.Pair, conf.OrderBookPrecision, book)
	api.WebSocket.SubscribeAccount(account)
	go api.WebSocket.Subscribe()

	// after api client successfully connect to remote web socket
	// channel will reveive current payload as separate messages.
	// each channel will receive order book updates: [price, count, ±amount]
	for {
		select {
		case t := <-trades:
			lastTrades = append(lastTrades[1:], t)
			//winTrades.Clear()
			//winTrades.Box(0, 0)
			//winTrades.Addstr(0, 0, "Last trades", 0)
			for n, t := range lastTrades {
				direction := "BUY"
				attr := int32(A_DIM)

				if math.Abs(t.Amount) > conf.HighlightTradesOver {
					attr |= A_BOLD
				}
				if t.Amount < 0 {
					direction = "SELL"
					attr |= Color_pair(clRed)
				} else {
					attr |= Color_pair(clGreen)
				}
				winTrades.Addstr(2, 1+n, fmt.Sprintf("%-4s %6.2f @ %-8.2f", direction, math.Abs(t.Amount), t.Price), attr)
			}
			if t.Amount < 0 {
				hist.SellAmount += t.Amount
			} else {
				hist.BuyAmount += t.Amount
			}
			updateHistory()

		case t := <-ticker:
			winTicker.Addstr(0, 0, fmt.Sprintf("Last: %-8.2f", t.LastPrice), Color_pair(clBlue)|A_BOLD)
			winTicker.Addstr(16, 0, fmt.Sprintf("Bid: %6.2f @ %-8.2f", t.BidSize, t.Bid), Color_pair(clRed))
			winTicker.Addstr(39, 0, fmt.Sprintf("Ask: %6.2f @ %-8.2f", t.AskSize, t.Ask), Color_pair(clGreen))

		case t := <-book:
			var book map[float64]*bitfinex.WebsocketBook
			var bookSorted *OrderBook
			var win *WinPanel
			if t.Amount > 0 {
				book = bookBid
				bookSorted = &bookBidSorted
				win = winBookBid
			} else {
				book = bookAsk
				bookSorted = &bookAskSorted
				win = winBookAsk
			}
			tt := new(bitfinex.WebsocketBook)
			*tt = t
			if t.Count == 0 {
				delete(book, t.Price)
			} else {
				book[t.Price] = tt
			}

			b := OrderBook{}
			for _, v := range book {
				b = append(b, v)
			}
			sort.Sort(ByPrice{b})
			*bookSorted = b

			cumulative := 0.0
			for n, v := range b {
				if n >= conf.OrderBookLen {
					break
				}
				amount := math.Abs(v.Amount)
				cumulative += amount

				attr := int32(0)
				if amount > conf.HighlightOrderBookOver {
					attr |= A_BOLD
				}
				if win == winBookBid {
					win.Addstr(2, 1+n, fmt.Sprintf("%2v %6.2f @ %-6.2f %8.2f", v.Count, amount, v.Price, cumulative), attr)
				} else {
					win.Addstr(2, 1+n, fmt.Sprintf("%-8.2f %6.2f @ %-6.2f %-2v", cumulative, v.Price, amount, v.Count), attr)
				}
			}
			updatePositions()

		case t := <-account:
			switch t := t.(type) {
			case bitfinex.WebsocketPosition:
				if t.Term() == "pc" {
					delete(positions, t.Pair)
					tablePositions.DeleteRow(t.Pair)
				} else {
					positions[t.Pair] = t
					updatePositions()
				}

			case bitfinex.WebsocketOrder:
				if t.Term() == "oc" {
					delete(orders, t.OrderID)
				} else {
					orders[t.OrderID] = t

				}
			}
		}

		if time.Since(hist.Timestamp) > conf.HistoryRecordPeriod*time.Second {
			history = append(history[1:], HistoryRecord{Timestamp: time.Now()})
			hist = &history[len(history)-1]
			updateHistory()
		}

		UpdatePanels()
		DoUpdate()
	}
}
