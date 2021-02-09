module github.com/zerospiel/portforward/cmd

go 1.16

replace github.com/zerospiel/portforward => ../

require (
	github.com/zerospiel/portforward v0.0.0-00010101000000-000000000000
	k8s.io/apimachinery v0.19.7
)
