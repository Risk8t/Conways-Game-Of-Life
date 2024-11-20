package gol

import (
	"fmt"
	"time"
	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events         chan<- Event
	ioCommand      chan<- ioCommand
	ioIdle         <-chan bool
	ioFilename     chan<- string
	ioOutput       chan<- uint8
	ioInput        <-chan uint8
	KeyPressesChan <-chan rune
}

func distributor(p Params, c distributorChannels) {
	c.ioCommand <- ioInput
	filename := fmt.Sprintf("%vx%v", p.ImageHeight, p.ImageWidth)
	c.ioFilename <- filename
	world := createBoard(p)
	receiveInput(c.ioInput, world)
	var initialAliveCells []util.Cell
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			if world[y][x] == 255 {
				initialAliveCells = append(initialAliveCells, util.Cell{X: x, Y: y})
			}
		}
	}
	c.events <- CellsFlipped{CompletedTurns: 0, Cells: initialAliveCells}
	c.events <- StateChange{CompletedTurns: 0, NewState: Executing}

	turn := 0
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	tempWorld := make([][]byte, 0)
	UnpausedChan := make(chan bool)
	pause := false
	newWorld := world
	worldChan := make(chan [][]byte, 1)
	turnChan := make(chan int, 1)
	quitChan := make(chan bool, 1)

	go func() {
		for {
			CalculatedWorld := <-worldChan
			CurrentTurn := <-turnChan
			select {
			case <-ticker.C:
				aliveCells := calculateAliveCells(p, CalculatedWorld)
				c.events <- AliveCellsCount{CompletedTurns: CurrentTurn, CellsCount: len(aliveCells)}
				UnpausedChan <- true
				quitChan <- false
			case keyPressed := <-c.KeyPressesChan:
				switch keyPressed {
				case 's':
					saveBoardAsPgm(p, c, CalculatedWorld, CurrentTurn)
					UnpausedChan <- true
					quitChan <- false
				case 'q':
					c.events <- FinalTurnComplete{CompletedTurns: CurrentTurn, Alive: calculateAliveCells(p, CalculatedWorld)}
					saveBoardAsPgm(p, c, CalculatedWorld, CurrentTurn)
					UnpausedChan <- true
					quitChan <- true
					return
				case 'p':
					pause = true
					c.events <- StateChange{CompletedTurns: CurrentTurn, NewState: Paused}
					for pause {
						key := <-c.KeyPressesChan
						switch key {
						case 'p':
							UnpausedChan <- true
							quitChan <- false
							pause = false
							c.events <- StateChange{CompletedTurns: CurrentTurn, NewState: Executing}
						case 's':
							saveBoardAsPgm(p, c, CalculatedWorld, CurrentTurn)
							if CurrentTurn == p.Turns {
								UnpausedChan <- true
								quitChan <- false
							}
						case 'q':
							c.events <- FinalTurnComplete{CompletedTurns: CurrentTurn, Alive: calculateAliveCells(p, CalculatedWorld)}
							saveBoardAsPgm(p, c, CalculatedWorld, CurrentTurn)
							UnpausedChan <- true
							quitChan <- true
							return
						}
					}
				}
			default:
				UnpausedChan <- true
				quitChan <- false
			}
		}
	}()

	for turns := 1; turns <= p.Turns; turns++ {
		var cellsToFlip []util.Cell
		heightChunk := p.ImageHeight / p.Threads
		in := make([]chan [][]byte, p.Threads)

		for i := 0; i < p.Threads; i++ {
			in[i] = make(chan [][]byte)
			startY := i * heightChunk
			endY := (i + 1) * heightChunk
			if i == p.Threads-1 {
				endY = p.ImageHeight
			}
			go worker(startY, endY, 0, p.ImageWidth, p, world, in[i])
		}

		newWorld = tempWorld
		for i := 0; i < p.Threads; i++ {
			part := <-in[i]
			newWorld = append(newWorld, part...)
		}

		for y := 0; y < p.ImageHeight; y++ {
			for x := 0; x < p.ImageWidth; x++ {
				if world[y][x] != newWorld[y][x] {
					cellsToFlip = append(cellsToFlip, util.Cell{X: x, Y: y})
				}
			}
		}

		c.events <- CellsFlipped{CompletedTurns: turns, Cells: cellsToFlip}

		world = newWorld
		turn = turns
		worldChan <- world
		turnChan <- turn
		c.events <- TurnComplete{CompletedTurns: turn}
		<-UnpausedChan
		if <-quitChan {
			c.events <- StateChange{CompletedTurns: turn, NewState: Quitting}
			close(c.events)
			return
		}
	}

	c.events <- FinalTurnComplete{CompletedTurns: p.Turns, Alive: calculateAliveCells(p, world)}
	c.ioCommand <- ioOutput
	filename = fmt.Sprintf("%vx%vx%v", p.ImageWidth, p.ImageHeight, p.Turns)
	c.ioFilename <- filename
	sendOutput(c.ioOutput, world)
	c.events <- ImageOutputComplete{CompletedTurns: p.Turns, Filename: filename}
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle
	c.events <- StateChange{turn, Quitting}
	close(c.events)
}

func worker(startY, endY, startX, endX int, p Params, world [][]byte, out chan<- [][]byte) {
	out <- calculateNextState(p, world, startY, endY, startX, endX)
}

func calculateNextState(p Params, world [][]byte, startY, endY, startX, endX int) [][]byte {
	height := endY - startY
	width := endX - startX

	neighbors := [8][2]int{
		{-1, -1}, {0, -1}, {1, -1},
		{-1, 0}, {1, 0},
		{-1, 1}, {0, 1}, {1, 1},
	}

	newWorld := make([][]byte, height)
	for i := range newWorld {
		newWorld[i] = make([]byte, width)
	}

	for i := 0; i < height; i++ {
		for j := 0; j < width; j++ {
			numberOfNeighborsAlive := 0

			for _, neighbor := range neighbors {
				neighborY := (i + startY + neighbor[0] + p.ImageHeight) % p.ImageHeight
				neighborX := (j + startX + neighbor[1] + p.ImageWidth) % p.ImageWidth
				if world[neighborY][neighborX] == 255 {
					numberOfNeighborsAlive++
				}
			}

			if world[startY+i][startX+j] == 255 { // Cell is alive
				if numberOfNeighborsAlive < 2 || numberOfNeighborsAlive > 3 {
					newWorld[i][j] = 0 // Dies
				} else {
					newWorld[i][j] = 255 // Stays alive
				}
			} else { // Cell is dead
				if numberOfNeighborsAlive == 3 {
					newWorld[i][j] = 255 // Becomes alive
				}
			}
		}
	}

	return newWorld
}

func calculateAliveCells(p Params, world [][]byte) []util.Cell {
	var aliveCells []util.Cell
	for i := 0; i < p.ImageHeight; i++ {
		for j := 0; j < p.ImageWidth; j++ {
			if world[i][j] == 255 {
				aliveCells = append(aliveCells, util.Cell{X: j, Y: i})
			}
		}
	}
	return aliveCells
}

func createBoard(p Params) [][]byte {
	world := make([][]byte, p.ImageHeight)
	for i := range world {
		world[i] = make([]byte, p.ImageWidth)
	}
	return world
}

func receiveInput(inputChan <-chan uint8, world [][]byte) {
	for i := range world {
		for j := range world[i] {
			world[i][j] = <-inputChan
		}
	}
}

func sendOutput(outputChan chan<- uint8, world [][]byte) {
	for i := range world {
		for j := range world[i] {
			outputChan <- world[i][j]
		}
	}
}

func saveBoardAsPgm(p Params, c distributorChannels, world [][]byte, turn int) {
	c.ioCommand <- ioOutput
	filename := fmt.Sprintf("%vx%vx%v", p.ImageWidth, p.ImageHeight, turn)
	c.ioFilename <- filename
	sendOutput(c.ioOutput, world)
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle
	c.events <- ImageOutputComplete{CompletedTurns: turn, Filename: filename}
}
