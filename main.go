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
)

type Config struct {
	ApiKey                 string
	ApiSecret              string
	OrderBookPrecision     string
	OrderBookLen           int
	PositionsLen           int
	OrdersLen              int
	HighlightTradesOver    float64
	HighlightOrderBookOver float64
}

var (
	conf Config

	winTicker, winTrades, winBookBid, winBookAsk, winPositions, winOrders *WinPanel

	lastTrades []bitfinex.WebsocketTrade
	bookBid    = make(map[float64]*bitfinex.WebsocketBook)
	bookAsk    = make(map[float64]*bitfinex.WebsocketBook)
	positions  = make(map[string]bitfinex.WebsocketPosition)
	orders     = make(map[int64]bitfinex.WebsocketOrder)

	bookBidSorted, bookAskSorted OrderBook
)

const (
	_ = iota
	clRed
	clGreen
	clBlue

	bookWidth   = 31
	tradesWidth = 25
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
	n := 0
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

		winPositions.Addstr(2, 1+n, fmt.Sprintf("%-6s %-7s", v.Pair, v.Status), 0)
		winPositions.Addstr(17, 1+n, fmt.Sprintf("%-6.2f  %-8.2f", v.Amount, v.Price), A_BOLD)
		winPositions.Addstr(37, 1+n, fmt.Sprintf("%-6.2f       %-9.2f %6.2f", value/v.Amount, profit, -(profit/baseValue)*100), attrPL)

		n++
		if n >= 3 {
			break
		}
	}
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
	lastTrades = make([]bitfinex.WebsocketTrade, conf.OrderBookLen)

	_, err = Initscr()
	if err != nil {
		log.Fatal("Unable to initialize ncurses:", err)
	}
	defer Endwin()
	Start_color()
	Init_pair(clRed, COLOR_RED, COLOR_BLACK)
	Init_pair(clGreen, COLOR_GREEN, COLOR_BLACK)
	Init_pair(clBlue, COLOR_BLUE, COLOR_BLACK)
	winTicker = NewWinPanel(1, 100, 0, 0, false, "Ticker")
	winBookBid = NewWinPanel(conf.OrderBookLen+2, bookWidth, 1, 0, true, "Bid")
	winBookAsk = NewWinPanel(conf.OrderBookLen+2, bookWidth, 1, bookWidth, true, "Ask")
	winTrades = NewWinPanel(conf.OrderBookLen+2, tradesWidth, 1, 2*bookWidth, true, "Last trades")
	winPositions = NewWinPanel(conf.PositionsLen+2, 2*bookWidth+tradesWidth, conf.OrderBookLen+2+1, 0, true, "  Pair   Status  Amount  Base price  Curr. price  P/L        P/L% ")
	winOrders = NewWinPanel(conf.OrdersLen+2, 2*bookWidth+tradesWidth, conf.OrderBookLen+conf.PositionsLen+2+2+1, 0, true, "  ID   Pair    Type   Orig.Amount   Amount   Price")

	UpdatePanels()
	DoUpdate()

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

	api.WebSocket.SubscribeTrades(bitfinex.BTCUSD, trades)
	api.WebSocket.SubscribeTicker(bitfinex.BTCUSD, ticker)
	api.WebSocket.SubscribeBook(bitfinex.BTCUSD, conf.OrderBookPrecision, book)
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
				} else {
					positions[t.Pair] = t
				}
				updatePositions()

			case bitfinex.WebsocketOrder:
				orders[t.OrderID] = t
			}
		}
		UpdatePanels()
		DoUpdate()
	}
}
