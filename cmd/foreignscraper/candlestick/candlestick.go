package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	models "github.com/diadata-org/diadata/pkg/model"
	ws "github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	tickerDurationSeconds = 60
	outlierBasisPoints    = float64(30)
	bufferSize            = 10000
)

type candlestickMessage struct {
	ForeignName string
	// ClosingPrice in USDT
	ClosingPrice float64
	Volume       float64
	Timestamp    time.Time
	ScrapeTime   time.Time
	Source       string
}

func getCandleStickMessageIdent(message candlestickMessage) string {
	return message.Source + "-" + message.ForeignName
}

func main() {

	wg := sync.WaitGroup{}
	ds, err := models.NewDataStore()
	if err != nil {
		log.Fatal("datastore error: ", err)
	}
	ticker := time.NewTicker(time.Duration(tickerDurationSeconds * time.Second))

	assets := flag.String("assets", "BTC,ETH", "asset symbols to query (from BTC, ETH, SOL, GLMR, DOT")
	exchanges := flag.String("exchanges", "Binance,GateIO,Kucoin,Huobi", "exchanges to query (from Binance, Kucoin, Coinbase, Huobi, Okex, GateIO")
	flag.Parse()
	cChan := make(chan candlestickMessage, bufferSize)

	for _, exchange := range strings.Split(*exchanges, ",") {
		wg.Add(1)
		go handleExchangeScraper(exchange, *assets, cChan, &wg)
	}
	defer wg.Wait()
	for t := range ticker.C {
		channelData := getRecentDataFromChannel(cChan, t)
		pairData := getPairData(channelData)
		log.Info("pair data: ", pairData)
		vwap, err := makeVWAP(pairData, outlierBasisPoints)
		if err != nil {
			log.Error("makeVWAP: ", err)
		}
		log.Info("vwap: ", vwap)
		for key, value := range vwap {
			err = ds.SetVWAP(key, value, t)
			if err != nil {
				log.Error("set VWAP: ", err)
			}
		}
	}
}

func handleExchangeScraper(exchange string, assets string, candleChan chan candlestickMessage, wg *sync.WaitGroup) {
	defer wg.Done()
	log.Printf("Entered Exchange handler for %s", exchange)
	for {
		switch exchange {
		case "Binance":
			log.Println("Binance Scraper: Start scraping")
			err := scrapeBinance(assets, candleChan)
			if err != nil {
				log.Error("Binance scraper: ", err)
			}
		case "GateIO":
			log.Println("Gateio Scraper: Start scraping")
			err := scrapeGateio(assets, candleChan)
			if err != nil {
				log.Error("GateIO scraper: ", err)
			}
		case "Kucoin":
			log.Println("Kucoin Scraper: Start scraping")
			err := scrapeKucoin(assets, candleChan)
			if err != nil {
				log.Error("Kucoin scraper: ", err)
			}
		case "Huobi":
			log.Println("Huobi Scraper: Start scraping")
			err := scrapeHuobi(assets, candleChan)
			if err != nil {
				log.Error("Huobi scraper: ", err)
			}
		case "OKEx":
			log.Println("OKEx Scraper: Start scraping")
			err := scrapeOkex(assets, candleChan)
			if err != nil {
				log.Error("OKEx scraper: ", err)
			}
		case "HitBTC":
			log.Println("HitBTC Scraper: Start scraping")
			err := scrapeHitbtc(assets, candleChan)
			if err != nil {
				log.Error("HitBTC scraper: ", err)
			}
		default:
			log.Errorf("Unknown scraper name %s", exchange)
			return
		}
		log.Info("Sleeping 30sec for exchange ", exchange)
		time.Sleep(30 * time.Second)
	}
}

func scrapeHitbtc(assets string, candleChan chan candlestickMessage) error {
	log.Info("Entered HitBTC handler")
	wsBaseString := "wss://api.hitbtc.com/api/3/ws/public"

	conn, _, err := ws.DefaultDialer.Dial(wsBaseString, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, asset := range strings.Split(assets, ",") {
		msgToWrite := fmt.Sprintf("{\"method\":\"subscribe\",\"ch\":\"candles/M1\",\"params\":{\"symbols\":[\"%sUSDT\"],\"limit\":10},\"id\": 1}", strings.ToUpper(asset))
		conn.WriteMessage(ws.TextMessage, []byte(msgToWrite))
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Errorln("read:", err)
			return err
		}
		//log.Printf("recv HitBTC: %s", message)
		messageMap := make(map[string]interface{})
		err = json.Unmarshal(message, &messageMap)
		if err != nil {
			return err
		}

		// Check if we got a status msg
		if messageMap["result"] != nil {
			continue
		}
		// or the initial snapshot
		if messageMap["snapshot"] != nil {
			continue
		}

		update := messageMap["update"].(map[string]interface{})
		assetKey := ""
		for k := range update {
			assetKey = k
			break
		}

		content := update[assetKey].([]interface{})
		result := content[0].(map[string]interface{})

		closingPriceString := result["c"].(string)
		closingPrice, err := strconv.ParseFloat(closingPriceString, 64)
		if err != nil {
			return err
		}
		volumeString := result["v"].(string)
		volume, err := strconv.ParseFloat(volumeString, 64)
		if err != nil {
			return err
		}
		timeUnix := result["t"].(float64)

		foreignNameString := assetKey

		candleStickMessage := candlestickMessage{
			ForeignName:  strings.ToUpper(foreignNameString),
			ClosingPrice: closingPrice,
			Volume:       volume,
			Timestamp:    time.Unix(int64(timeUnix)/1000, 0),
			ScrapeTime:   time.Now(),
			Source:       "HitBTC",
		}

		candleChan <- candleStickMessage
	}
}

func scrapeOkex(assets string, candleChan chan candlestickMessage) error {
	log.Info("Entered OKExhandler")
	wsBaseString := "wss://real.okex.com:8443/ws/v3"

	conn, _, err := ws.DefaultDialer.Dial(wsBaseString, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, asset := range strings.Split(assets, ",") {
		msgToWrite := fmt.Sprintf("{\"op\":\"subscribe\",\"args\":[\"spot/candle60s:%s-USDT\"]}", strings.ToUpper(asset))
		conn.WriteMessage(ws.TextMessage, []byte(msgToWrite))
	}

	for {
		_, zippedMessage, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		bytesReader := bytes.NewReader([]byte(zippedMessage))
		gzreader := flate.NewReader(bytesReader)

		message, err := ioutil.ReadAll(gzreader)
		if err != nil {
			log.Errorln("read:", err)
			return err
		}
		//log.Printf("recv OKEx: %s", message)
		messageMap := make(map[string]interface{})
		err = json.Unmarshal(message, &messageMap)
		if err != nil {
			return err
		}
		// Check if we got a status msg
		if messageMap["table"] != "spot/candle60s" {
			continue
		}
		data := messageMap["data"].([]interface{})
		result := data[0].(map[string]interface{})
		candle := result["candle"].([]interface{})

		closingPriceString := candle[4].(string)
		closingPrice, err := strconv.ParseFloat(closingPriceString, 64)
		if err != nil {
			return err
		}
		volumeString := candle[5].(string)
		volume, err := strconv.ParseFloat(volumeString, 64)
		if err != nil {
			return err
		}
		timeIso := candle[0].(string)
		layout := "2006-01-02T15:04:05.000Z"
		timeParsed, err := time.Parse(layout, timeIso)
		if err != nil {
			return err
		}

		foreignNameString := result["instrument_id"].(string)
		foreignNameFiltered := strings.Split(foreignNameString, "-")[0]

		candleStickMessage := candlestickMessage{
			ForeignName:  foreignNameFiltered + "USDT",
			ClosingPrice: closingPrice,
			Volume:       volume,
			Timestamp:    timeParsed,
			ScrapeTime:   time.Now(),
			Source:       "OKEx",
		}

		candleChan <- candleStickMessage

	}
}

func scrapeBinance(assets string, candleChan chan candlestickMessage) error {
	log.Info("Entered Binance handler")
	wsBaseString := "wss://stream.binance.com:9443/stream?streams="
	wsAssetsString := ""

	for _, asset := range strings.Split(assets, ",") {
		wsAssetsString += strings.ToLower(asset) + "usdt@kline_1m" + "/"
	}
	// Remove trailing slash
	wsAssetsString = wsAssetsString[:len(wsAssetsString)-1]
	conn, _, err := ws.DefaultDialer.Dial(wsBaseString+wsAssetsString, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Errorln("read:", err)
			return err
		}
		//log.Printf("recv Binance: %s", message)
		messageMap := make(map[string]interface{})
		err = json.Unmarshal(message, &messageMap)
		data := messageMap["data"].(map[string]interface{})["k"].(map[string]interface{})
		timeUnix := messageMap["data"].(map[string]interface{})["E"].(float64)

		closingPriceString := data["c"].(string)
		closingPrice, err := strconv.ParseFloat(closingPriceString, 64)
		volumeString := data["V"].(string)
		volume, err := strconv.ParseFloat(volumeString, 64)

		candleStickMessage := candlestickMessage{
			ForeignName:  data["s"].(string),
			ClosingPrice: closingPrice,
			Volume:       volume,
			Timestamp:    time.Unix(int64(timeUnix/1000), 0),
			ScrapeTime:   time.Now(),
			Source:       "Binance",
		}

		candleChan <- candleStickMessage

	}
}

func scrapeGateio(assets string, candleChan chan candlestickMessage) error {
	log.Info("Entered GateIO handler")
	wsBaseString := "wss://api.gateio.ws/ws/v4/"

	conn, _, err := ws.DefaultDialer.Dial(wsBaseString, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, asset := range strings.Split(assets, ",") {
		msgToWrite := fmt.Sprintf("{\"time\":30,\"channel\":\"spot.candlesticks\",\"event\":\"subscribe\",\"payload\":[\"1m\",\"%s_USD\"]}", asset)
		conn.WriteMessage(ws.TextMessage, []byte(msgToWrite))
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Errorln("read:", err)
			return err
		}
		//log.Printf("recv GateIO: %s", message)
		messageMap := make(map[string]interface{})
		err = json.Unmarshal(message, &messageMap)
		if err != nil {
			return err
		}
		if messageMap["error"] != nil {
			log.Errorf("error GateIO: %s", message)
			continue
		}
		result := messageMap["result"].(map[string]interface{})

		// Check if we got a status msg
		if result["status"] != nil {
			continue
		}

		closingPriceString := result["c"].(string)
		closingPrice, err := strconv.ParseFloat(closingPriceString, 64)
		if err != nil {
			return err
		}
		volumeString := result["v"].(string)
		volume, err := strconv.ParseFloat(volumeString, 64)
		if err != nil {
			return err
		}
		timeUnixString := result["t"].(string)
		timeUnix, err := strconv.ParseFloat(timeUnixString, 64)
		if err != nil {
			return err
		}

		foreignNameString := result["n"].(string)
		foreignNameFiltered := strings.Split(foreignNameString, "_")[1]

		candleStickMessage := candlestickMessage{
			ForeignName:  strings.ToUpper(foreignNameFiltered) + "USDT",
			ClosingPrice: closingPrice,
			Volume:       volume,
			Timestamp:    time.Unix(int64(timeUnix), 0),
			ScrapeTime:   time.Now(),
			Source:       "GateIO",
		}

		candleChan <- candleStickMessage
	}
}

func scrapeKucoin(assets string, candleChan chan candlestickMessage) error {
	log.Info("Entered Kucoin handler")
	wsBaseString := "wss://ws-api.kucoin.com/endpoint?token="

	// Get token
	tokenUrl := "https://api.kucoin.com/api/v1/bullet-public"
	resp, err := http.Post(tokenUrl, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	bodyMap := make(map[string]interface{})
	err = json.Unmarshal(body, &bodyMap)
	if err != nil {
		return err
	}
	token := bodyMap["data"].(map[string]interface{})["token"].(string)

	conn, _, err := ws.DefaultDialer.Dial(wsBaseString+token, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	for _, asset := range strings.Split(assets, ",") {
		msgToWrite := fmt.Sprintf("{\"id\":1,\"type\":\"subscribe\",\"topic\": \"/market/candles:%s-USDT_1min\",\"response\": true}", strings.ToUpper(asset))
		conn.WriteMessage(ws.TextMessage, []byte(msgToWrite))
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Errorln("read:", err)
			return err
		}
		//log.Printf("recv Kucoin: %s", message)
		messageMap := make(map[string]interface{})
		err = json.Unmarshal(message, &messageMap)
		if err != nil {
			return err
		}
		// Check if we got a status msg
		if messageMap["type"] != "message" {
			continue
		}
		result := messageMap["data"].(map[string]interface{})
		candles := result["candles"].([]interface{})

		closingPriceString := candles[2].(string)
		closingPrice, err := strconv.ParseFloat(closingPriceString, 64)
		if err != nil {
			return err
		}
		volumeString := candles[5].(string)
		volume, err := strconv.ParseFloat(volumeString, 64)
		if err != nil {
			return err
		}
		timeUnix := result["time"].(float64)
		timeUnix /= 1e9

		foreignNameString := result["symbol"].(string)
		foreignNameFiltered := strings.Split(foreignNameString, "-")[0]

		candleStickMessage := candlestickMessage{
			ForeignName:  foreignNameFiltered + "USDT",
			ClosingPrice: closingPrice,
			Volume:       volume,
			Timestamp:    time.Unix(int64(timeUnix), 0),
			ScrapeTime:   time.Now(),
			Source:       "Kucoin",
		}

		// send a ping for every msg TODO: set to timer
		msgToWrite := fmt.Sprintf("{\"id\":%d,\"type\":\"ping\"}", 1)
		conn.WriteMessage(ws.TextMessage, []byte(msgToWrite))

		candleChan <- candleStickMessage

	}
}

func scrapeHuobi(assets string, candleChan chan candlestickMessage) error {
	log.Info("Entered Huobi handler")
	wsBaseString := "wss://api.huobi.pro/ws"

	conn, _, err := ws.DefaultDialer.Dial(wsBaseString, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	for _, asset := range strings.Split(assets, ",") {
		msgToWrite := fmt.Sprintf("{\"sub\":\"market.%susdt.kline.1min\",\"id\":\"id1\"}", strings.ToLower(asset))
		conn.WriteMessage(ws.TextMessage, []byte(msgToWrite))
	}

	for {
		_, zippedMessage, err := conn.ReadMessage()
		bytesReader := bytes.NewReader([]byte(zippedMessage))
		gzreader, err := gzip.NewReader(bytesReader)
		if err != nil {
			return err
		}

		message, err := ioutil.ReadAll(gzreader)
		if err != nil {
			log.Errorln("read:", err)
			return err
		}
		//log.Printf("recv Huobi: %s", message)

		messageMap := make(map[string]interface{})
		err = json.Unmarshal(message, &messageMap)
		if err != nil {
			return err
		}

		// Check if we got a ping
		if messageMap["ping"] != nil {
			pingNumber := int(messageMap["ping"].(float64))
			msgToWrite := fmt.Sprintf("{\"pong\":%d}", pingNumber)
			conn.WriteMessage(ws.TextMessage, []byte(msgToWrite))
			continue
		}

		if messageMap["tick"] == nil {
			continue
		}

		result := messageMap["tick"].(map[string]interface{})

		closingPrice := result["close"].(float64)
		volume := result["amount"].(float64)
		timeUnix := messageMap["ts"].(float64)
		timeUnix /= 1e3

		foreignNameString := messageMap["ch"].(string)
		foreignNameFiltered := strings.Split(foreignNameString, ".")[1]

		candleStickMessage := candlestickMessage{
			ForeignName:  strings.ToUpper(foreignNameFiltered),
			ClosingPrice: closingPrice,
			Volume:       volume,
			Timestamp:    time.Unix(int64(timeUnix), 0),
			ScrapeTime:   time.Now(),
			Source:       "Huobi",
		}

		candleChan <- candleStickMessage
	}
}

// getRecentDataFromChannel returns the most recent data for each identifier
// Source-ForeignName sitting in the channel @candleChan. It reads data from the channel
// until the first data point hits @endtime.
func getRecentDataFromChannel(candleChan chan candlestickMessage, endtime time.Time) map[string]candlestickMessage {
	lastCandleData := make(map[string]candlestickMessage)
	for message := range candleChan {
		// log.Info("message: ", message)
		// Channels are passed by reference. As the channel is continuously written to,
		// we need to stop fetching from it as soon as endtime is passed.
		if message.Timestamp.After(endtime) {
			return lastCandleData
		}
		if endtime.Sub(message.ScrapeTime) > time.Duration(tickerDurationSeconds*time.Second) {
			continue
		}
		messageIdent := getCandleStickMessageIdent(message)
		if _, ok := lastCandleData[messageIdent]; !ok {
			lastCandleData[messageIdent] = message
		} else {
			if message.Timestamp.After(lastCandleData[messageIdent].Timestamp) {
				lastCandleData[messageIdent] = message
			}
		}
	}
	return lastCandleData
}

// makeCandle returns the VWAP of a trading pair in USD.
// It discards everything above 30 basis points from the median.
func getPairData(candleData map[string]candlestickMessage) map[string][]candlestickMessage {
	pairData := make(map[string][]candlestickMessage)
	for key, value := range candleData {
		pair := strings.Split(key, "-")[1]
		if _, ok := pairData[pair]; !ok {
			pairData[pair] = []candlestickMessage{value}
		} else {
			pairData[pair] = append(pairData[pair], value)
		}
	}
	return pairData
}

// makeVWAP takes a map with foreign names as keys and []candlestickMessage as values,
// containing all values of the underlying pair across sources, i.e. (at most) one value per source.
func makeVWAP(pairData map[string][]candlestickMessage, threshold float64) (map[string]float64, error) {
	vwapMap := make(map[string]float64)
	for key, value := range pairData {
		cleanedPrices, cleanedVolumes, err := discardOutliers(getPrices(value), getVolumes(value), threshold)
		if err != nil {
			return vwapMap, err
		}
		vwapMap[key], err = vwap(cleanedPrices, cleanedVolumes)
		if err != nil {
			return vwapMap, err
		}
	}
	return vwapMap, nil
}

// vwap returns the volume weighted average price for the slices @prices and @volumes.
func vwap(prices []float64, volumes []float64) (float64, error) {
	//log.Info("prices, volumes: ", prices, volumes)
	if len(prices) != len(volumes) {
		return 0, errors.New("number of prices does not equal number of volumes ")
	}
	avg := float64(0)
	totalVolume := float64(0)
	for i := 0; i < len(prices); i++ {
		avg += prices[i] * math.Abs(volumes[i])
		totalVolume += math.Abs(volumes[i])
	}
	if totalVolume > 0 {
		return avg / totalVolume, nil
	} else {
		return 0, nil
	}
}

// discardOutliers discards every data point from @prices and @volumes that deviates from
// the price median by more than @basispoints basis points.
func discardOutliers(prices []float64, volumes []float64, basispoints float64) (newPrices []float64, newVolumes []float64, err error) {
	if len(prices) != len(volumes) {
		err = errors.New("number of prices does not equal number of volumes ")
		return
	}
	median := computeMedian(prices)
	threshold := basispoints * float64(0.0001) * median
	for i := 0; i < len(prices); i++ {
		if math.Abs(prices[i]-median) < threshold {
			newPrices = append(newPrices, prices[i])
			newVolumes = append(newVolumes, volumes[i])
		}
	}
	return
}

// computeMedian returns the median of @samples.
func computeMedian(samples []float64) (median float64) {
	var length = len(samples)
	if length > 0 {
		sort.Float64s(samples)
		if length%2 == 0 {
			median = (samples[length/2-1] + samples[length/2]) / 2
		} else {
			median = samples[(length+1)/2-1]
		}
	}
	return
}

func getPrices(messages []candlestickMessage) (prices []float64) {
	for _, message := range messages {
		prices = append(prices, message.ClosingPrice)
	}
	return
}

func getVolumes(messages []candlestickMessage) (volumes []float64) {
	for _, message := range messages {
		volumes = append(volumes, message.Volume)
	}
	return
}
