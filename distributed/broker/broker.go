package main

import (
	"flag"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"
	"uk.ac.bris.cs/gameoflife/gol/stubs"
)

type GolOperation struct {
	previousWorld      [][]byte
	NumberOfCellsAlive int
	CurrTurns          int
	mutex              sync.Mutex
	CurrentWorld       [][]byte
	paused             bool
	quit               bool
	kill               bool
	end                chan bool
	Continue           bool
	workers            []string
	matrixChan         chan [][]byte
	CompletedTurnChan  chan int
	previousWorldChan  chan [][]byte
	killChan           chan bool
}

func (g *GolOperation) CalculateGameOfLife(req *stubs.Request, res *stubs.Response) error {
	g.mutex.Lock()
	g.quit = false
	g.CurrentWorld = req.Matrix
	g.CurrTurns = req.CurrTurns
	g.previousWorld = g.CurrentWorld
	g.paused = false
	g.kill = false
	g.mutex.Unlock()
	workerNum := len(g.workers)
	clients := make([]*rpc.Client, len(g.workers))
	for i := 0; i < workerNum; i++ {
		clients[i], _ = rpc.Dial("tcp", g.workers[i])
	}

	for !g.quit && g.CurrTurns < req.NumberOfTurns {
		g.mutex.Lock()
		if !g.paused {
			tempWorld := make([][]byte, 0)
			previousWorld := g.CurrentWorld
			heightChunk := req.Height / workerNum
			chunkReqs := make([]*stubs.Request, workerNum)
			chunkRes := make([]*stubs.Response, workerNum)
			done := make([]chan *rpc.Call, workerNum)
			for i := 0; i < workerNum; i++ {
				startY := i * heightChunk
				endY := (i + 1) * heightChunk
				if i == workerNum-1 {
					endY = req.Height
				}
				chunkReqs[i] = &stubs.Request{
					StartY: startY, EndY: endY,
					StartX: 0, EndX: req.Width,
					Matrix: g.CurrentWorld,
					Height: req.Height, Width: req.Width,
					Threads: req.Threads,
				}
				chunkRes[i] = &stubs.Response{}
				done[i] = make(chan *rpc.Call, 1)
				clients[i].Go(stubs.CalculateGameOfLife, chunkReqs[i], chunkRes[i], done[i])
			}

			for i := 0; i < workerNum; i++ {
				<-done[i]
				tempWorld = append(tempWorld, chunkRes[i].Matrix...)
			}

			if !g.kill && g.CurrTurns < req.NumberOfTurns-1 {
				g.killChan <- false
			} else {
				g.killChan <- true
			}
			g.previousWorldChan <- previousWorld
			g.CurrentWorld = tempWorld
			g.CurrTurns++
			g.CompletedTurnChan <- g.CurrTurns
			g.matrixChan <- g.CurrentWorld
		}
		g.NumberOfCellsAlive = reportNumberOfAliveCells(req.Height, req.Width, g.CurrentWorld)
		g.mutex.Unlock()
	}

	g.mutex.Lock()
	res.Matrix = g.CurrentWorld
	res.CurrTurns = g.CurrTurns
	g.mutex.Unlock()
	return nil
}

func AliveCells(world [][]byte) int {
	var aliveCells int
	for i := 0; i < len(world); i++ {
		for j := 0; j < len(world[i]); j++ {
			if world[i][j] == 255 {
				aliveCells++
			}
		}
	}
	return aliveCells
}

func (g *GolOperation) ReportTurnsAndWorld(req *stubs.ReportRequest, res *stubs.ReportResponse) error {
	res.Kill = <-g.killChan
	res.PreviousWorld = <-g.previousWorldChan
	res.CurrTurns = <-g.CompletedTurnChan
	res.Matrix = <-g.matrixChan
	return nil
}

func (g *GolOperation) ReportNumberOfAliveCells(req *stubs.Request, res *stubs.NewResponse) (err error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	res.NumberOfCellsAlive = reportNumberOfAliveCells(req.Height, req.Width, g.CurrentWorld)
	res.CurrTurns = g.CurrTurns
	return

}

func (g *GolOperation) KeyPressHandle(req *stubs.KeyPressRequest, res *stubs.NewResponse) (err error) {
	server := []string{"172.31.32.209:8030", "172.31.40.156:8040", "172.31.38.150:8050", "172.31.47.67:8060"}

	switch req.KeyPress {
	case 'k':
		g.mutex.Lock()
		g.quit = true
		g.kill = true
		g.mutex.Unlock()
		go func() {
			time.Sleep(2 * time.Second)
			os.Exit(0)
		}()
		for i := 0; i < len(server); i++ {
			serverClient, _ := rpc.Dial("tcp", server[i])
			defer serverClient.Close()
			killRequest := &stubs.KillRequest{}
			killResponse := &stubs.KillResponse{}
			serverClient.Call(stubs.KillSever, killRequest, killResponse)
		}

	case 'q':
		g.mutex.Lock()
		g.Continue = true
		g.quit = true
		g.previousWorld = g.CurrentWorld
		g.mutex.Unlock()
	case 'p':
		g.mutex.Lock()
		if req.Paused {

			g.paused = true

		} else {
			g.paused = false
			res.Message = "Continuing"
		}
		g.mutex.Unlock()
	}
	res.CurrWorld = g.CurrentWorld
	res.CurrTurns = g.CurrTurns
	return
}

func reportNumberOfAliveCells(height, width int, world [][]byte) int {
	var numberOfAliveCells int
	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			if world[i][j] == 255 {
				numberOfAliveCells++
			}
		}
	}
	return numberOfAliveCells
}

func (g *GolOperation) GetContinue(req stubs.Request, res *stubs.Response) (err error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	res.Matrix = g.CurrentWorld
	res.Continue = g.Continue
	res.CurrTurns = g.CurrTurns
	return
}

func main() {
	workerAddr := []string{"172.31.32.209:8030", "172.31.40.156:8040", "172.31.38.150:8050", "172.31.47.67:8060"}
	addrPtr := flag.String("port", ":8020", "IP:port string to connect to")
	flag.Parse()
	golOp := &GolOperation{Continue: false}
	golOp.matrixChan = make(chan [][]byte, 1)
	golOp.CompletedTurnChan = make(chan int, 1)
	golOp.previousWorldChan = make(chan [][]byte, 1)
	golOp.workers = make([]string, 4)
	golOp.killChan = make(chan bool, 1)
	golOp.workers = workerAddr

	rpc.Register(golOp)
	listener, _ := net.Listen("tcp", *addrPtr)
	defer listener.Close()
	rpc.Accept(listener)
}
