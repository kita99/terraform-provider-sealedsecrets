package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/kita99/terraform-provider-sealedsecrets/sealedsecrets"
)

func main() {
    opts := &plugin.ServeOpts{ProviderFunc: sealedsecrets.Provider}
    plugin.Serve(opts)
}
