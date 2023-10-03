package main

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func main() {
	generate_digest()
	// err := godotenv.Load()
	// if err != nil {
	// 	log.Fatal("Error loading .env file")
	// }

	// pref := tele.Settings{
	// 	Token:  os.Getenv("TG_TOKEN"),
	// 	Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	// }

	// b, err := tele.NewBot(pref)
	// if err != nil {
	// 	log.Fatal(err)
	// 	return
	// }

	// b.Handle("/start", func(c tele.Context) error {
	// 	return c.Send("Hello!")
	// })

	// b.Start()
}

type ArticleInfoSelectors struct {
	Title           string
	Link            string
	ArticleSelector string
	ArticleTitle    string
	ArticleDate     string
	ArticleLink     string
}

type ArticleInfo struct {
	SectionTitle string
	Title        string
	Link         string
	Date         string
}

var linksToElements = []ArticleInfoSelectors{
	{
		Title:           "ZWP Online - Dental News",
		Link:            "https://www.zwp-online.info/zwpnews/dental-news",
		ArticleSelector: ".articles article",
		ArticleTitle:    ".content_wrapper a h3",
		ArticleDate:     ".small_subline span",
		ArticleLink:     ".content_wrapper a",
	},
	{
		Title:           "Zahnmedizin Online - Alle News",
		Link:            "https://www.zm-online.de/news/alle-news",
		ArticleSelector: ".newslist article",
		ArticleTitle:    ".news-text-wrap h2",
		ArticleDate:     ".meta-info-wrap time",
		ArticleLink:     "a",
	},
	{
		Title:           "ZMK Aktuell - Fachgebiete",
		Link:            "https://www.zmk-aktuell.de/fachgebiete.html",
		ArticleSelector: "#articles .row.article",
		ArticleTitle:    ".large-8.columns a h2",
		ArticleDate:     ".subtitle div",
		ArticleLink:     "a",
	},
	{
		Title:           "ZMK Aktuell - Junge Zahnärzte",
		Link:            "https://www.zmk-aktuell.de/junge-zahnaerzte.html",
		ArticleSelector: "#articles .row.article",
		ArticleTitle:    ".large-8.columns a h2",
		ArticleDate:     ".subtitle div",
		ArticleLink:     "a",
	},
	{
		Title:           "DZW - Zahnmedizin",
		Link:            "https://dzw.de/zahnmedizin",
		ArticleSelector: ".list article",
		ArticleTitle:    ".teaser__headline h2 span",
		ArticleDate:     ".teaser__authored span.date",
		ArticleLink:     ".teaser__headline a",
	},
}

func generate_digest() {
	// client := openai.NewClient(os.Getenv("OPENAI_TOKEN"))

	// Create a channel for each link
	channels := make([]chan []ArticleInfo, len(linksToElements))
	for i := range channels {
		channels[i] = make(chan []ArticleInfo)
	}

	// Spawn a goroutine for each channel
	for i, obj := range linksToElements {
		go func(i int, obj ArticleInfoSelectors) {
			articles := processPage(&obj)
			channels[i] <- articles
		}(i, obj)
	}

	var articles []ArticleInfo

	// Collect the results from each channel
	for _, ch := range channels {
		articles = append(articles, <-ch...)
	}

	// close the channels
	for _, ch := range channels {
		defer close(ch)
	}

	// sort the articles by date
	sort.Slice(articles, func(i, j int) bool {
		// convert date string to time
		layout := "02.01.2006"
		t1, err := time.Parse(layout, articles[i].Date)
		if err != nil {
			log.Fatal(err)
		}
		t2, err := time.Parse(layout, articles[j].Date)
		if err != nil {
			log.Fatal(err)
		}
		return t2.Before(t1)
	})

	// filter out articles that are not in 7 days range from current date
	var filteredArticles []ArticleInfo
	for _, article := range articles {
		// convert date string to time
		layout := "02.01.2006"
		t, err := time.Parse(layout, article.Date)
		if err != nil {
			log.Fatal(err)
		}
		// get the difference between current date and article date
		diff := time.Until(t)
		// if the difference is less than 7 days, add it to the filtered articles
		if diff.Abs().Hours() <= 168 {
			filteredArticles = append(filteredArticles, article)
		}

	}
	log.Println(len(filteredArticles))

}

func processPage(obj *ArticleInfoSelectors) []ArticleInfo {
	res, err := http.Get(obj.Link)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		log.Fatalf("status code error: %d %s", res.StatusCode, res.Status)
	}

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	var articles []ArticleInfo

	// Find the review items
	doc.Find(obj.ArticleSelector).Each(func(i int, s *goquery.Selection) {
		// For each item found, get the band and title
		title := strings.TrimSpace(s.Find(obj.ArticleTitle).Text())
		link, _ := s.Find(obj.ArticleLink).Attr("href")
		link = strings.TrimSpace(link)
		date := strings.TrimSpace(s.Find(obj.ArticleDate).First().Text())
		if date != "" {
			articles = append(articles, ArticleInfo{SectionTitle: obj.Title, Title: title, Link: obj.Link + link, Date: date})
		}

	})
	return articles
}
