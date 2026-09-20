module github.com/jrgf/go-vial/examples/websocket

go 1.26.6

require (
	github.com/coder/websocket v1.8.15
	github.com/jrgf/go-vial v1.0.0-rc.1
	github.com/jrgf/go-vial/vialws v0.0.0
)

replace github.com/jrgf/go-vial => ../..

replace github.com/jrgf/go-vial/vialws => ../../vialws
