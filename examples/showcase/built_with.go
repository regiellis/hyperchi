package main

import "github.com/regiellis/hyperchi/hyperchi"

// builtWithSite is one production site on the /showcase page. Image is a
// 16:10 screenshot of the live site's first screen under static/img/showcase/,
// stored at 1200x750.
type builtWithSite struct {
	Name        string
	Kind        string
	URL         string
	Host        string
	Image       string
	Alt         string
	Description string
}

// builtWithSites lists sites that run on HyperChi in production. Descriptions
// follow each site's own meta description.
var builtWithSites = []builtWithSite{
	{
		Name:        "Playlogic",
		Kind:        "Studio site",
		URL:         "https://playlogic.io/",
		Host:        "playlogic.io",
		Image:       "/static/img/showcase/playlogic.webp",
		Alt:         "The Playlogic homepage: GAMES WITH A LITTLE BITE in black on a yellow field, beside a play-button mark",
		Description: "An independent game studio making small, sharp games you can learn in a minute and keep coming back to.",
	},
	{
		Name:        "Dueling Dice",
		Kind:        "Game site",
		URL:         "https://duelingdice.playlogic.io/",
		Host:        "duelingdice.playlogic.io",
		Image:       "/static/img/showcase/dueling-dice.webp",
		Alt:         "The Dueling Dice homepage: the DUELING DICE title on plum felt, ringed by the rivals' faces",
		Description: "A dice battler from Playlogic: six dice, three rounds to win, five ways to play, and nine rivals to read. Out now on PC.",
	},
	{
		Name:        "HyperChi",
		Kind:        "This site",
		URL:         "https://hyperchi.regiellis.com/",
		Host:        "hyperchi.regiellis.com",
		Image:       "/static/img/showcase/hyperchi-docs.webp",
		Alt:         "The HyperChi homepage: Stay close to the web, over a network of connected points",
		Description: "The landing page, the live playground, and the documentation, with search and llms.txt, all served by one HyperChi app.",
	},
}

func setupBuiltWith(app *hyperchi.HyperChi) {
	app.Get("/showcase", func(c *hyperchi.Context) error {
		return c.View("built-with.tmpl", hyperchi.H{
			"title":       "Built with HyperChi",
			"description": "Sites running on HyperChi in production: Playlogic, Dueling Dice, and this site.",
			"current":     "built",
			"sites":       builtWithSites,
		})
	})
}
