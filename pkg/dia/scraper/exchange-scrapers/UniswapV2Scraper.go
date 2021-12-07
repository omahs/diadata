package scrapers

import (
	"encoding/json"
	"errors"
	"github.com/diadata-org/diadata/pkg/dia/scraper/exchange-scrapers/uniswap"
	"io/ioutil"
	"math"
	"math/big"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/diadata-org/diadata/pkg/dia"
	"github.com/diadata-org/diadata/pkg/dia/helpers"
	"github.com/diadata-org/diadata/pkg/dia/helpers/configCollectors"
	"github.com/diadata-org/diadata/pkg/utils"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	exchangeFactoryContractAddress = "0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f"
	reversePairs                   *[]string
	mainBaseAssets                 = []string{
		"0xdAC17F958D2ee523a2206206994597C13D831ec7",
	}
)

const (
	restDial = "http://159.69.120.42:8545/"
	wsDial   = "ws://159.69.120.42:8546/"

	restDialBSC = ""
	wsDialBSC   = ""

	restDialPolygon = ""
	wsDialPolygon   = ""

	restDialCelo = ""
	wsDialCelo   = ""

	restDialFantom = ""
	wsDialFantom   = ""

	restDialMoonriver = ""
	wsDialMoonriver   = ""

	restDialAurora = ""
	wsDialAurora   = ""

	uniswapWaitMilliseconds     = "25"
	sushiswapWaitMilliseconds   = "100"
	pancakeswapWaitMilliseconds = "600"
	dfynWaitMilliseconds        = "100"
	quickswapWaitMilliseconds   = "100"
	ubeswapWaitMilliseconds     = "200"
	spookyswapWaitMilliseconds  = "200"
	solarbeamWaitMilliseconds   = "400"
	trisolarisWaitMilliseconds  = "200"
)

type UniswapToken struct {
	Address  common.Address
	Symbol   string
	Decimals uint8
	Name     string
}

type UniswapPair struct {
	Token0      UniswapToken
	Token1      UniswapToken
	ForeignName string
	Address     common.Address
}

type UniswapSwap struct {
	ID         string
	Timestamp int64
	Pair      UniswapPair
	Amount0In float64
	Amount0Out float64
	Amount1In  float64
	Amount1Out float64
}

type UniswapScraper struct {
	WsClient   *ethclient.Client
	RestClient *ethclient.Client
	// signaling channels for session initialization and finishing
	//initDone     chan nothing
	run          bool
	shutdown     chan nothing
	shutdownDone chan nothing
	// error handling; to read error or closed, first acquire read lock
	// only cleanup method should hold write lock
	errorLock sync.RWMutex
	error     error
	closed    bool
	// used to keep track of trading pairs that we subscribed to
	pairScrapers map[string]*UniswapPairScraper
	exchangeName string
	chanTrades   chan *dia.Trade
	waitTime     int
}

// NewUniswapScraper returns a new UniswapScraper for the given pair
func NewUniswapScraper(exchange dia.Exchange, scrape bool) *UniswapScraper {
	log.Info("NewUniswapScraper: ", exchange.Name)
	var wsClient, restClient *ethclient.Client
	var waitTime int
	var err error

	switch exchange.Name {
	case dia.UniswapExchange:
		exchangeFactoryContractAddress = exchange.Contract.Hex()
		restClient, err = ethclient.Dial(utils.Getenv("ETH_URI_REST", restDial))
		if err != nil {
			log.Fatal(err)
		}

		wsClient, err = ethclient.Dial(utils.Getenv("ETH_URI_WS", wsDial))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("UNISWAP_WAIT_TIME", uniswapWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 100
		}

	case dia.SushiSwapExchange:
		exchangeFactoryContractAddress = exchange.Contract.Hex()
		wsClient, err = ethclient.Dial(utils.Getenv("ETH_URI_WS", wsDial))
		if err != nil {
			log.Fatal(err)
		}

		restClient, err = ethclient.Dial(utils.Getenv("ETH_URI_REST", restDial))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("SUSHISWAP_WAIT_TIME", sushiswapWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 100
		}

	case dia.PanCakeSwap:
		log.Infoln("Init ws and rest client for BSC chain")
		wsClient, err = ethclient.Dial(utils.Getenv("ETH_URI_WS_BSC", wsDialBSC))
		if err != nil {
			log.Fatal("dial websocket: ", err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("ETH_URI_REST_BSC", restDialBSC))
		if err != nil {
			log.Fatal("dial rest client: ", err)
		}
		waitTimeString := utils.Getenv("PANCAKESWAP_WAIT_TIME", pancakeswapWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 600
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	case dia.DfynNetwork:
		log.Infoln("Init ws and rest client for Polygon chain")
		wsClient, err = ethclient.Dial(utils.Getenv("POLYGON_URI_WS", wsDialPolygon))
		if err != nil {
			log.Fatal(err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("POLYGON_URI_REST", restDialPolygon))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("DFYN_WAIT_TIME", dfynWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 100
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	case dia.QuickswapExchange:
		log.Infoln("Init ws and rest client for Polygon chain")
		wsClient, err = ethclient.Dial(utils.Getenv("POLYGON_URI_WS", wsDialPolygon))
		if err != nil {
			log.Fatal(err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("POLYGON_URI_REST", restDialPolygon))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("QUICKSWAP_WAIT_TIME", quickswapWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 100
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	case dia.UbeswapExchange:
		log.Infoln("Init ws and rest client for CELO chain")
		wsClient, err = ethclient.Dial(utils.Getenv("CELO_URI_WS", wsDialCelo))
		if err != nil {
			log.Fatal(err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("CELO_URI_REST", restDialCelo))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("CELO_WAIT_TIME", ubeswapWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 100
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	case dia.SpookyswapExchange:
		log.Infoln("Init ws and rest client for Fantom chain")
		wsClient, err = ethclient.Dial(utils.Getenv("FANTOM_URI_WS", wsDialFantom))
		if err != nil {
			log.Fatal(err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("FANTOM_URI_REST", restDialFantom))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("FANTOM_WAIT_TIME", spookyswapWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 100
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	case dia.SpiritswapExchange:
		log.Infoln("Init ws and rest client for Fantom chain")
		wsClient, err = ethclient.Dial(utils.Getenv("FANTOM_URI_WS", wsDialFantom))
		if err != nil {
			log.Fatal(err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("FANTOM_URI_REST", restDialFantom))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("FANTOM_WAIT_TIME", spookyswapWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 100
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	case dia.SolarbeamExchange:
		log.Infoln("Init ws and rest client for Moonbeam chain")
		wsClient, err = ethclient.Dial(utils.Getenv("MOONRIVER_URI_WS", wsDialMoonriver))
		if err != nil {
			log.Fatal(err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("MOONRIVER_URI_REST", restDialMoonriver))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("MOONRIVER_WAIT_TIME", solarbeamWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 400
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	case dia.TrisolarisExchange:
		log.Infoln("Init ws and rest client for Aurora chain")
		wsClient, err = ethclient.Dial(utils.Getenv("AURORA_URI_WS", wsDialAurora))
		if err != nil {
			log.Fatal(err)
		}
		restClient, err = ethclient.Dial(utils.Getenv("AURORA_URI_REST", restDialAurora))
		if err != nil {
			log.Fatal(err)
		}
		waitTimeString := utils.Getenv("AURORA_WAIT_TIME", trisolarisWaitMilliseconds)
		waitTime, err = strconv.Atoi(waitTimeString)
		if err != nil {
			log.Error("could not parse wait time: ", err)
			waitTime = 200
		}
		exchangeFactoryContractAddress = exchange.Contract.Hex()

	}

	s := &UniswapScraper{
		shutdown:     make(chan nothing),
		shutdownDone: make(chan nothing),
		pairScrapers: make(map[string]*UniswapPairScraper),
		exchangeName: exchange.Name,
		error:        nil,
		chanTrades:   make(chan *dia.Trade),
		waitTime:     waitTime,
	}

	s.WsClient = wsClient
	s.RestClient = restClient
	if scrape {
		go s.mainLoop()
	}
	return s
}

// runs in a goroutine until s is closed
func (s *UniswapScraper) mainLoop() {

	// Import tokens which appear as base token and we need a quotation for
	var err error
	reversePairs, err = getReverseTokensFromConfig("uniswap/reverse_tokens")
	if err != nil {
		log.Error("error getting tokens for which pairs should be reversed: ", err)
	}

	// wait for all pairs have added into s.PairScrapers
	time.Sleep(4 * time.Second)
	s.run = true

	numPairs, err := s.getNumPairs()
	if err != nil {
		log.Fatal(err)
	}
	log.Info("Found ", numPairs, " pairs")
	log.Info("Found ", len(s.pairScrapers), " pairScrapers")

	if len(s.pairScrapers) == 0 {
		s.error = errors.New("uniswap: No pairs to scrap provided")
		log.Error(s.error.Error())
	}

	var wg sync.WaitGroup
	for i := 0; i < numPairs; i++ {
		time.Sleep(time.Duration(s.waitTime) * time.Millisecond)
		wg.Add(1)
		go func(index int, w *sync.WaitGroup) {
			defer w.Done()
			s.ListenToPairByIndex(index)
		}(i, &wg)
	}
	wg.Wait()
}

func (s *UniswapScraper) ListenToPairByIndex(i int) {
	var pair UniswapPair
	var err error
	if i == -1 && s.exchangeName == "PanCakeSwap" {
		token0 := UniswapToken{
			Address:  common.HexToAddress("0x4DA996C5Fe84755C80e108cf96Fe705174c5e36A"),
			Symbol:   "WOW",
			Decimals: uint8(18),
		}
		token1 := UniswapToken{
			Address:  common.HexToAddress("0xe9e7CEA3DedcA5984780Bafc599bD69ADd087D56"),
			Symbol:   "BUSD",
			Decimals: uint8(18),
		}
		pair = UniswapPair{
			Token0:      token0,
			Token1:      token1,
			ForeignName: "WOW-BUSD",
			Address:     common.HexToAddress("0xA99b9bCC6a196397DA87FA811aEd293B1b488f44"),
		}
	} else {
		pair, err = s.GetPairByID(int64(i))
		if err != nil {
			log.Error("error fetching pair: ", err)
		}
	}
	if len(pair.Token0.Symbol) < 2 || len(pair.Token1.Symbol) < 2 {
		log.Info("skip pair: ", pair.ForeignName)
		return
	}
	// if helpers.SymbolIsBlackListed(pair.Token0.Symbol) || helpers.SymbolIsBlackListed(pair.Token1.Symbol) {
	// 	if helpers.SymbolIsBlackListed(pair.Token0.Symbol) {
	// 		log.Infof("skip pair %s. symbol %s is blacklisted", pair.ForeignName, pair.Token0.Symbol)
	// 	} else {
	// 		log.Infof("skip pair %s. symbol %s is blacklisted", pair.ForeignName, pair.Token1.Symbol)
	// 	}
	// 	return
	// }
	if helpers.AddressIsBlacklisted(pair.Token0.Address) || helpers.AddressIsBlacklisted(pair.Token1.Address) {
		log.Info("skip pair ", pair.ForeignName, ", address is blacklisted")
		return
	}

	// Normalize WETH on Ethereum
	if Exchanges[s.exchangeName].BlockChain.Name == dia.ETHEREUM {
		pair.normalizeUniPair()
	}
	// ps := s.pairScrapers[pair.ForeignName]
	// if ok {
	log.Info(i, ": add pair scraper for: ", pair.ForeignName, " with address ", pair.Address.Hex())
	sink, err := s.GetSwapsChannel(pair.Address)
	if err != nil {
		log.Error("error fetching swaps channel: ", err)
	}

	go func() {
		for {
			rawSwap, ok := <-sink
			if ok {
				swap, err := s.normalizeUniswapSwap(*rawSwap)
				if err != nil {
					log.Error("error normalizing swap: ", err)
				}
				price, volume := getSwapData(swap)
				token0 := dia.Asset{
					Address:    pair.Token0.Address.Hex(),
					Symbol:     pair.Token0.Symbol,
					Name:       pair.Token0.Name,
					Decimals:   pair.Token0.Decimals,
					Blockchain: Exchanges[s.exchangeName].BlockChain.Name,
				}
				token1 := dia.Asset{
					Address:    pair.Token1.Address.Hex(),
					Symbol:     pair.Token1.Symbol,
					Name:       pair.Token1.Name,
					Decimals:   pair.Token1.Decimals,
					Blockchain: Exchanges[s.exchangeName].BlockChain.Name,
				}
				t := &dia.Trade{
					Symbol:         pair.Token0.Symbol,
					Pair:           pair.ForeignName,
					Price:          price,
					Volume:         volume,
					BaseToken:      token1,
					QuoteToken:     token0,
					Time:           time.Unix(swap.Timestamp, 0),
					ForeignTradeID: swap.ID,
					Source:         s.exchangeName,
					VerifiedPair:   true,
				}
				// If we need quotation of a base token, reverse pair
				if utils.Contains(reversePairs, pair.Token1.Address.Hex()) {
					tSwapped, err := dia.SwapTrade(*t)
					if err == nil {
						t = &tSwapped
					}
				}
				// Reverse almost all pairs ETH-XXX on Uniswap and Sushiswap
				if s.exchangeName == dia.UniswapExchange || s.exchangeName == dia.SushiSwapExchange {
					if token0.Address == "0x0000000000000000000000000000000000000000" && !utils.Contains(&mainBaseAssets, token1.Address) {
						tSwapped, err := dia.SwapTrade(*t)
						if err == nil {
							t = &tSwapped
						}
					}
				}
				if price > 0 {
					log.Info("tx hash: ", swap.ID)
					log.Infof("Got trade at time %v - symbol: %s, pair: %s, price: %v, volume:%v", t.Time, t.Symbol, t.Pair, t.Price, t.Volume)
					// log.Infof("Base token info --- Symbol: %s - Address: %s - Blockchain: %s ", t.BaseToken.Symbol, t.BaseToken.Address, t.BaseToken.Blockchain)
					// log.Info("----------------")
					s.chanTrades <- t
				}
				if price == 0 {
					log.Info("tx hash: ", swap.ID)
					log.Info("Got zero trade: ", t)
				}
			}
		}
	}()
}

// GetSwapsChannel returns a channel for swaps of the pair with address @pairAddress
func (s *UniswapScraper) GetSwapsChannel(pairAddress common.Address) (chan *uniswap.UniswapV2PairSwap, error) {

	sink := make(chan *uniswap.UniswapV2PairSwap)
	var pairFiltererContract *uniswap.UniswapV2PairFilterer
	pairFiltererContract, err := uniswap.NewUniswapV2PairFilterer(pairAddress, s.WsClient)
	if err != nil {
		log.Fatal(err)
	}

	_, err = pairFiltererContract.WatchSwap(&bind.WatchOpts{}, sink, []common.Address{}, []common.Address{})
	if err != nil {
		log.Error("error in get swaps channel: ", err)
	}

	return sink, nil

}

// getReverseTokensFromConfig returns a list of addresses from config file.
func getReverseTokensFromConfig(filename string) (*[]string, error) {

	var reverseTokens []string

	// Load file and read data
	filehandle := configCollectors.ConfigFileConnectors(filename, ".json")
	jsonFile, err := os.Open(filehandle)
	if err != nil {
		return &[]string{}, err
	}
	defer func() {
		err = jsonFile.Close()
		if err != nil {
			log.Error(err)
		}
	}()

	byteData, err := ioutil.ReadAll(jsonFile)
	if err != nil {
		return &[]string{}, err
	}

	// Unmarshal read data
	type lockedAsset struct {
		Address string `json:"Address"`
		Symbol  string `json:"Symbol"`
	}
	type lockedAssetList struct {
		AllAssets []lockedAsset `json:"Tokens"`
	}
	var allAssets lockedAssetList
	err = json.Unmarshal(byteData, &allAssets)
	if err != nil {
		return &[]string{}, err
	}

	// Extract addresses
	for _, token := range allAssets.AllAssets {
		reverseTokens = append(reverseTokens, token.Address)
	}

	return &reverseTokens, nil
}

// normalizeUniswapSwap takes a swap as returned by the swap contract's channel and converts it to a UniswapSwap type
func (s *UniswapScraper) normalizeUniswapSwap(swap uniswap.UniswapV2PairSwap) (normalizedSwap UniswapSwap, err error) {

	pair, err := s.GetPairByAddress(swap.Raw.Address)
	if err != nil {
		log.Error("error getting pair by address: ", err)
		return
	}
	decimals0 := int(pair.Token0.Decimals)
	decimals1 := int(pair.Token1.Decimals)
	amount0In, _ := new(big.Float).Quo(big.NewFloat(0).SetInt(swap.Amount0In), new(big.Float).SetFloat64(math.Pow10(decimals0))).Float64()
	amount0Out, _ := new(big.Float).Quo(big.NewFloat(0).SetInt(swap.Amount0Out), new(big.Float).SetFloat64(math.Pow10(decimals0))).Float64()
	amount1In, _ := new(big.Float).Quo(big.NewFloat(0).SetInt(swap.Amount1In), new(big.Float).SetFloat64(math.Pow10(decimals1))).Float64()
	amount1Out, _ := new(big.Float).Quo(big.NewFloat(0).SetInt(swap.Amount1Out), new(big.Float).SetFloat64(math.Pow10(decimals1))).Float64()

	normalizedSwap = UniswapSwap{
		ID:         swap.Raw.TxHash.Hex(),
		Timestamp:  time.Now().Unix(),
		Pair:       pair,
		Amount0In:  amount0In,
		Amount0Out: amount0Out,
		Amount1In:  amount1In,
		Amount1Out: amount1Out,
	}
	return
}

// pairHealthCheck returns true if the involved tokens are not blacklisted and do not have zero entries
func (up *UniswapPair) pairHealthCheck() bool {
	if up.Token0.Symbol == "" || up.Token1.Symbol == "" || up.Token0.Address.Hex() == "" || up.Token1.Address.Hex() == "" {
		return false
	}
	if helpers.SymbolIsBlackListed(up.Token0.Symbol) || helpers.SymbolIsBlackListed(up.Token1.Symbol) {
		if helpers.SymbolIsBlackListed(up.Token0.Symbol) {
			log.Infof("skip pair %s. symbol %s is blacklisted", up.ForeignName, up.Token0.Symbol)
		} else {
			log.Infof("skip pair %s. symbol %s is blacklisted", up.ForeignName, up.Token1.Symbol)
		}
		return false
	}
	if helpers.AddressIsBlacklisted(up.Token0.Address) || helpers.AddressIsBlacklisted(up.Token1.Address) {
		log.Info("skip pair ", up.ForeignName, ", address is blacklisted")
		return false
	}
	return true
}

// FetchAvailablePairs returns a list with all available trade pairs as dia.ExchangePair for the pairDiscorvery service
func (s *UniswapScraper) FetchAvailablePairs() (pairs []dia.ExchangePair, err error) {
	time.Sleep(100 * time.Millisecond)
	uniPairs, err := s.GetAllPairs()
	if err != nil {
		return
	}
	for _, pair := range uniPairs {
		if !pair.pairHealthCheck() {
			continue
		}
		quotetoken := dia.Asset{
			Symbol:     pair.Token0.Symbol,
			Name:       pair.Token0.Name,
			Address:    pair.Token0.Address.Hex(),
			Decimals:   pair.Token0.Decimals,
			Blockchain: Exchanges[s.exchangeName].BlockChain.Name,
		}
		basetoken := dia.Asset{
			Symbol:     pair.Token1.Symbol,
			Name:       pair.Token1.Name,
			Address:    pair.Token1.Address.Hex(),
			Decimals:   pair.Token1.Decimals,
			Blockchain: Exchanges[s.exchangeName].BlockChain.Name,
		}
		pairToNormalise := dia.ExchangePair{
			Symbol:         pair.Token0.Symbol,
			ForeignName:    pair.ForeignName,
			Exchange:       "UniswapV2",
			Verified:       true,
			UnderlyingPair: dia.Pair{BaseToken: basetoken, QuoteToken: quotetoken},
		}
		normalizedPair, _ := s.NormalizePair(pairToNormalise)
		pairs = append(pairs, normalizedPair)
	}

	return
}

// FillSymbolData is not used by DEX scrapers.
func (s *UniswapScraper) FillSymbolData(symbol string) (dia.Asset, error) {
	return dia.Asset{}, nil
}

// GetAllPairs is similar to FetchAvailablePairs. But instead of dia.ExchangePairs it returns all pairs as UniswapPairs,
// i.e. including the pair's address
func (s *UniswapScraper) GetAllPairs() ([]UniswapPair, error) {
	time.Sleep(20 * time.Millisecond)
	connection := s.RestClient
	var contract *uniswap.IUniswapV2FactoryCaller
	contract, err := uniswap.NewIUniswapV2FactoryCaller(common.HexToAddress(exchangeFactoryContractAddress), connection)
	if err != nil {
		log.Error(err)
	}

	numPairs, err := contract.AllPairsLength(&bind.CallOpts{})
	if err != nil {
		return []UniswapPair{}, err
	}
	wg := sync.WaitGroup{}
	defer wg.Wait()
	pairs := make([]UniswapPair, int(numPairs.Int64()))
	for i := 0; i < int(numPairs.Int64()); i++ {
		if s.exchangeName == dia.PanCakeSwap {
			time.Sleep(time.Duration(s.waitTime) * time.Millisecond)
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			uniPair, err := s.GetPairByID(int64(index))
			if err != nil {
				log.Error("error retrieving pair by ID: ", err)
				return
			}
			uniPair.normalizeUniPair()
			pairs[index] = uniPair
		}(i)
	}
	return pairs, nil
}

func (up *UniswapScraper) NormalizePair(pair dia.ExchangePair) (dia.ExchangePair, error) {
	return pair, nil
}

// Account for WETH is identified with ETH
func (up *UniswapPair) normalizeUniPair() {
	if up.Token0.Address == common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2") {
		up.Token0.Symbol = "ETH"
		up.Token0.Address = common.HexToAddress("0x0000000000000000000000000000000000000000")
		up.ForeignName = up.Token0.Symbol + "-" + up.Token1.Symbol
	}
	if up.Token1.Address == common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2") {
		up.Token1.Symbol = "ETH"
		up.Token1.Address = common.HexToAddress("0x0000000000000000000000000000000000000000")
		up.ForeignName = up.Token0.Symbol + "-" + up.Token1.Symbol
	}
}

// GetPairByID returns the UniswapPair with the integer id @num
func (s *UniswapScraper) GetPairByID(num int64) (UniswapPair, error) {
	log.Info("Get pair ID: ", num)
	var contract *uniswap.IUniswapV2FactoryCaller
	contract, err := uniswap.NewIUniswapV2FactoryCaller(common.HexToAddress(exchangeFactoryContractAddress), s.RestClient)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}
	numToken := big.NewInt(num)
	pairAddress, err := contract.AllPairs(&bind.CallOpts{}, numToken)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}

	pair, err := s.GetPairByAddress(pairAddress)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}
	return pair, err
}

// GetPairByAddress returns the UniswapPair with pair address @pairAddress
func (s *UniswapScraper) GetPairByAddress(pairAddress common.Address) (pair UniswapPair, err error) {
	connection := s.RestClient
	var pairContract *uniswap.IUniswapV2PairCaller
	pairContract, err = uniswap.NewIUniswapV2PairCaller(pairAddress, connection)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}

	// Getting tokens from pair ---------------------
	address0, _ := pairContract.Token0(&bind.CallOpts{})
	address1, _ := pairContract.Token1(&bind.CallOpts{})
	var token0Contract *uniswap.IERC20Caller
	var token1Contract *uniswap.IERC20Caller
	token0Contract, err = uniswap.NewIERC20Caller(address0, connection)
	if err != nil {
		log.Error(err)
	}
	token1Contract, err = uniswap.NewIERC20Caller(address1, connection)
	if err != nil {
		log.Error(err)
	}
	symbol0, err := token0Contract.Symbol(&bind.CallOpts{})
	if err != nil {
		log.Error(err)
	}
	symbol1, err := token1Contract.Symbol(&bind.CallOpts{})
	if err != nil {
		log.Error(err)
	}
	decimals0, err := s.GetDecimals(address0)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}
	decimals1, err := s.GetDecimals(address1)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}

	name0, err := s.GetName(address0)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}
	name1, err := s.GetName(address1)
	if err != nil {
		log.Error(err)
		return UniswapPair{}, err
	}
	token0 := UniswapToken{
		Address:  address0,
		Symbol:   symbol0,
		Decimals: decimals0,
		Name:     name0,
	}
	token1 := UniswapToken{
		Address:  address1,
		Symbol:   symbol1,
		Decimals: decimals1,
		Name:     name1,
	}
	foreignName := symbol0 + "-" + symbol1
	pair = UniswapPair{
		ForeignName: foreignName,
		Address:     pairAddress,
		Token0:      token0,
		Token1:      token1,
	}
	return pair, nil
}

// GetDecimals returns the decimals of the token with address @tokenAddress
func (s *UniswapScraper) GetDecimals(tokenAddress common.Address) (decimals uint8, err error) {

	var contract *uniswap.IERC20Caller
	contract, err = uniswap.NewIERC20Caller(tokenAddress, s.RestClient)
	if err != nil {
		log.Error(err)
		return
	}
	decimals, err = contract.Decimals(&bind.CallOpts{})

	return
}

func (s *UniswapScraper) GetName(tokenAddress common.Address) (name string, err error) {

	var contract *uniswap.IERC20Caller
	contract, err = uniswap.NewIERC20Caller(tokenAddress, s.RestClient)
	if err != nil {
		log.Error(err)
		return
	}
	name, err = contract.Name(&bind.CallOpts{})

	return
}

// getNumPairs returns the number of available pairs on Uniswap
func (s *UniswapScraper) getNumPairs() (int, error) {

	var contract *uniswap.IUniswapV2FactoryCaller
	contract, err := uniswap.NewIUniswapV2FactoryCaller(common.HexToAddress(exchangeFactoryContractAddress), s.RestClient)
	if err != nil {
		log.Error(err)
	}

	// Getting pairs ---------------
	numPairs, err := contract.AllPairsLength(&bind.CallOpts{})
	if err != nil {
		return 0, err
	}
	return int(numPairs.Int64()), err
}

// getSwapData returns price, volume and sell/buy information of @swap
func getSwapData(swap UniswapSwap) (price float64, volume float64) {
	if swap.Amount0In == float64(0) {
		volume = swap.Amount0Out
		price = swap.Amount1In / swap.Amount0Out
		return
	}
	volume = -swap.Amount0In
	price = swap.Amount1Out / swap.Amount0In
	return
}

// Close closes any existing API connections, as well as channels of
// PairScrapers from calls to ScrapePair
func (s *UniswapScraper) Close() error {
	if s.closed {
		return errors.New("UniswapScraper: Already closed")
	}
	s.WsClient.Close()
	s.RestClient.Close()
	close(s.shutdown)
	<-s.shutdownDone
	s.errorLock.RLock()
	defer s.errorLock.RUnlock()
	return s.error
}

// ScrapePair returns a PairScraper that can be used to get trades for a single pair from
// this APIScraper
func (s *UniswapScraper) ScrapePair(pair dia.ExchangePair) (PairScraper, error) {
	s.errorLock.RLock()
	defer s.errorLock.RUnlock()
	if s.error != nil {
		return nil, s.error
	}
	if s.closed {
		return nil, errors.New("UniswapScraper: Call ScrapePair on closed scraper")
	}
	ps := &UniswapPairScraper{
		parent: s,
		pair:   pair,
	}
	s.pairScrapers[pair.ForeignName] = ps
	return ps, nil
}

// UniswapPairScraper implements PairScraper for Uniswap
type UniswapPairScraper struct {
	parent *UniswapScraper
	pair   dia.ExchangePair
	closed bool
}

// Close stops listening for trades of the pair associated with s
func (ps *UniswapPairScraper) Close() error {
	ps.closed = true
	return nil
}

// Channel returns a channel that can be used to receive trades
func (ps *UniswapScraper) Channel() chan *dia.Trade {
	return ps.chanTrades
}

// Error returns an error when the channel Channel() is closed
// and nil otherwise
func (ps *UniswapPairScraper) Error() error {
	s := ps.parent
	s.errorLock.RLock()
	defer s.errorLock.RUnlock()
	return s.error
}

// Pair returns the pair this scraper is subscribed to
func (ps *UniswapPairScraper) Pair() dia.ExchangePair {
	return ps.pair
}