package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	godotenv "github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
	tele "gopkg.in/telebot.v3"
)

// a map with chat ids whose request is being processed right now
var processing = make(map[int64]bool)

var cachedDigests = make(map[int64]CachedDigest)

type CachedDigest struct {
	ChatId      int64
	Digest      string
	DateCreated time.Time
}

func splitPrompt(text string, splitLength int) ([]string, error) {
	if splitLength <= 0 {
		return nil, fmt.Errorf("max length must be greater than 0")
	}

	numParts := int(math.Ceil(float64(len(text)) / float64(splitLength)))
	fileData := make([]string, numParts)

	for i := 0; i < numParts; i++ {
		start := i * splitLength
		end := int(math.Min(float64((i+1)*splitLength), float64(len(text))))

		var content string
		if i == numParts-1 {
			content = fmt.Sprintf("[START PART %d/%d]\n%s\n[END PART %d/%d]\nALL PARTS SENT. Now you can continue processing the request.", i+1, numParts, text[start:end], i+1, numParts)
		} else {
			content = fmt.Sprintf("Do not answer yet. This is just another part of the text I want to send you. Just receive and acknowledge as \"Part %d/%d received\" and wait for the next part.\n[START PART %d/%d]\n%s\n[END PART %d/%d]\nRemember not answering yet. Just acknowledge you received this part with the message \"Part %d/%d received\" and wait for the next part.", i+1, numParts, i+1, numParts, text[start:end], i+1, numParts, i+1, numParts)
		}

		fileData[i] = content
	}

	return fileData, nil
}

func splitDigest(text string, splitLength int) []string {
	// split the text by \n\n
	splitted := strings.Split(text, "\n\n")
	var result []string
	for _, text := range splitted {
		if len(text) <= splitLength {
			result = append(result, text)
		} else {
			// split the text by \n
			splittedByNewLine := strings.Split(text, "\n")
			var temp string
			for _, text := range splittedByNewLine {
				if len(temp)+len(text) <= splitLength {
					temp = temp + text + "\n"
				} else {
					result = append(result, temp)
					temp = text + "\n"
				}
			}
			result = append(result, temp)
		}
	}

	return result
}

func formatDigestAsHtml(digestdata []DigestContent) string {
	// filter out empty digests
	filtered := []DigestContent{}
	for _, digest := range digestdata {
		if digest.Content != "" {
			filtered = append(filtered, digest)
		}
	}
	var html string
	for idx, digest := range filtered {
		html = html + fmt.Sprintf(
			"<b>%d.</b> ", idx+1,
		) + "\n" + digest.Link + "\n" + "<strong>" + digest.Title + "</strong>" + "\n" + digest.Content + "<a href=\"" + digest.Link + "\">" + "</a>" + "\n" + "<b>" + digest.Date + "</b>" + "\n\n"
	}
	return html
}

func validateChatCache(chatId int64) bool {
	if _, ok := cachedDigests[chatId]; ok {
		println("cached digest found")
		// check if the digest is older than 1 day
		diff := time.Until(cachedDigests[chatId].DateCreated)
		if diff.Abs().Hours() <= 24 {
			return false
		} else {
			return true
		}
	}
	return true
}

func main() {
	godotenv.Load(".env")

	client := openai.NewClient(os.Getenv("OPENAI_TOKEN"))
	log.Printf("client: %v", client)
	pref := tele.Settings{
		Token:  os.Getenv("TG_TOKEN"),
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	log.Printf("bot: %v", b)
	if err != nil {
		log.Fatal(err)
		return
	}

	b.Handle("/start", func(c tele.Context) error {

		return c.Send("Welcome to the Zahnmedizin News Bot, my luv!. Send the /generate command to get the latest news in the field of dentistry")
	})

	b.Handle("/generate", func(c tele.Context) error {
		if processing[c.Chat().ID] {
			return c.Send("Your request is being processed. Please wait until it is finished")
		}

		processing[c.Chat().ID] = true

		if !validateChatCache(c.Chat().ID) {
			processing[c.Chat().ID] = false

			return c.Send("Your digest is older than 24 hours. Please wait until it is updated")
		}

		c.Send("Please wait until your request is processed. It may take up to 5 minutes")

		articles := get_articles()

		c.Send("Found " + fmt.Sprintf("%d", len(articles)) + " articles for the last week")

		if len(articles) == 0 {
			processing[c.Chat().ID] = false
			return c.Send("No articles for the last week found")
		}

		digestdata := make([]DigestContent, len(articles))
		for idx, article := range articles {
			// wait for 5 seconds
			time.Sleep(1 * time.Second)

			c.Send("Processing article " + fmt.Sprintf("%d", idx+1) + "/" + fmt.Sprintf("%d", len(articles)))

			// create a prompt
			prompt := "This is a summary of an article. Please create a short digest of maximum 1 sentence in german language only of the following article. Make the output as concise and comprehensive, though as short as possible. Please respond only with the content of the digest and nothing else. Exclude article name " + article.SectionTitle + ":\n\n" + article.Title + "\n\n" + article.Content + "\n\n"

			texts, err := splitPrompt(prompt, 2000)
			if err != nil {
				log.Fatal(err)
			}
			for _, text := range texts {
				log.Printf("text: %v", len(text))
			}
			prompts := make([]openai.ChatCompletionMessage, len(texts))
			for i, text := range texts {
				prompts[i] = openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleUser,
					Content: text,
				}
			}

			var chathistory []openai.ChatCompletionMessage
			for _, prompt := range prompts {

				chathistory = append(chathistory, prompt)
				completion, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
					Model:    openai.GPT3Dot5Turbo16K,
					Messages: chathistory,
				})
				if err != nil {
					log.Fatal(err)
				}
				chathistory = append(chathistory, completion.Choices[0].Message)
			}

			chathistory = append(chathistory, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: "please create a digest of the article and output it your response without any other text",
			})

			completion, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
				Model:    openai.GPT3Dot5Turbo16K,
				Messages: chathistory,
			})
			if err != nil {
				log.Fatal(err)
			}
			digestdata = append(digestdata, DigestContent{
				Title:   article.Title,
				Content: strings.TrimSpace(completion.Choices[0].Message.Content),
				Link:    article.Link,
				Date:    article.Date,
			})

		}

		formattedDigest := formatDigestAsHtml(digestdata)
		cachedDigests[c.Chat().ID] = CachedDigest{
			ChatId:      c.Chat().ID,
			Digest:      formattedDigest,
			DateCreated: time.Now(),
		}
		processing[c.Chat().ID] = false
		splittedDigest := splitDigest(formattedDigest, 4096)
		for _, digest := range splittedDigest {
			c.Send(digest, &tele.SendOptions{ParseMode: tele.ModeHTML})
		}
		println("digest sent")
		return nil
		// return c.Send(formattedDigest, &tele.SendOptions{ParseMode: tele.ModeHTML})
	})

	b.Start()
}

// func create_cron_job(c tele.Context, client *openai.Client) {
// 	cron := cron.New()

// 	// every 1 minute
// 	cron.AddFunc("*/10 * * * * *", func() {

// 	})
// }

func get_articles() []ArticleContent {
	meta := get_article_meta()

	// remove duplicates
	seen := make(map[string]bool)
	var unique []ArticleInfo
	for _, article := range meta {
		if _, ok := seen[article.Link]; !ok {
			unique = append(unique, article)
			seen[article.Link] = true
		}
	}

	texts := get_article_contents(unique)

	return texts
}

const (
	Normal = "normal"
	Frame  = "frame"
	None   = "none"
)

type ArticleInfoSelectors struct {
	Title               string
	Link                string
	Pagination          string
	IsJsPager           bool
	CookieConsent       string
	FrameConsent        string
	CookieType          string
	ArticleSelector     string
	ArticleTitle        string
	ArticleDate         string
	ArticleLink         string
	ArticleTextSelector string
	ErrorPage           string
}

type ArticleInfo struct {
	SectionTitle        string
	Title               string
	Link                string
	Date                string
	ArticleTextSelector string
	CookieConsent       string
	FrameConsent        string
	CookieType          string
	ErrorPage           string
}

type ArticleContent struct {
	SectionTitle string
	Title        string
	Link         string
	Date         string
	Content      string
}

type DigestContent struct {
	Title   string
	Content string
	Link    string
	Date    string
}

var linksToElements = []ArticleInfoSelectors{
	{
		Title:               "ZWP Online - Dental News",
		Link:                "https://www.zwp-online.info/zwpnews/dental-news",
		ArticleSelector:     ".articles article",
		ArticleTitle:        ".content_wrapper a h3",
		Pagination:          ".pagination li:has(a:not([rel=\"prev\"]))",
		CookieConsent:       "#cookiesEnabled",
		CookieType:          Normal,
		IsJsPager:           false,
		ArticleDate:         ".small_subline span",
		ArticleLink:         ".content_wrapper a",
		ArticleTextSelector: ".detail_text",
	},
	{
		Title:               "Zahnmedizin Online - Alle News",
		Link:                "https://www.zm-online.de/news/alle-news",
		Pagination:          ".button-wrap.frame-space-before-m",
		IsJsPager:           true,
		CookieType:          Frame,
		CookieConsent:       "button[title=\"Akzeptieren\"]",
		FrameConsent:        "#sp_message_iframe_712588",
		ArticleSelector:     ".newslist .article.hover-effect.articletype-0.clearfix",
		ArticleTitle:        ".news-text-wrap h2",
		ArticleDate:         ".meta-info-wrap time",
		ArticleLink:         "a",
		ArticleTextSelector: ".bodytext",
		ErrorPage:           ".typo3-error-page",
	},
	{
		Title:               "ZMK Aktuell - Fachgebiete",
		Link:                "https://www.zmk-aktuell.de/fachgebiete.html",
		ArticleSelector:     "#articles .row.article",
		Pagination:          ".pagination li:not(.arrow) a",
		IsJsPager:           false,
		CookieType:          Normal,
		CookieConsent:       "#acceptallcookies",
		FrameConsent:        "#cookiebanner",
		ArticleTitle:        ".large-8.columns a h2",
		ArticleDate:         ".subtitle div",
		ArticleLink:         "a",
		ArticleTextSelector: "#article-detail",
	},
	{
		Title:               "ZMK Aktuell - Junge Zahnärzte",
		Link:                "https://www.zmk-aktuell.de/junge-zahnaerzte.html",
		ArticleSelector:     "#articles .row.article",
		ArticleTitle:        ".large-8.columns a h2",
		Pagination:          ".pagination li a",
		IsJsPager:           false,
		ArticleDate:         ".subtitle div",
		ArticleLink:         "a",
		ArticleTextSelector: "#article-detail",
	},
	{
		Title:               "DZW - Zahnmedizin",
		Link:                "https://dzw.de/zahnmedizin",
		ArticleSelector:     ".list article",
		Pagination:          ".pager__items.js-pager__items li a",
		IsJsPager:           false,
		ArticleTitle:        ".teaser__headline span",
		ArticleDate:         ".teaser__authored span.date",
		ArticleLink:         ".teaser__headline a",
		CookieType:          None,
		ArticleTextSelector: "#block-mainpagecontent > article > div > div > article > div.article-content-wrapper > div > div",
	},
}

func get_article_contents(meta []ArticleInfo) []ArticleContent {
	// process articles in parallel in chunks of 5
	var articles []ArticleContent

	// Create a channel for each link

	// Spawn a goroutine for each channel
	for idx, obj := range meta {

		println(idx)
		article, err := getArticleText(&obj)
		if err != nil {
			continue
		}
		articles = append(articles, article)
	}

	return articles
}

func getArticleText(obj *ArticleInfo) (ArticleContent, error) {

	path, _ := launcher.LookPath()
	u := launcher.New().Bin(path).MustLaunch()
	println(u)
	browser := rod.New().ControlURL(u).MustConnect()
	page := browser.MustPage()

	err := rod.Try(func() {
		page.Timeout(15 * time.Second).MustNavigate(obj.Link)
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return ArticleContent{}, err
	}
	defer func() {
		page.MustClose()
		browser.MustClose()
	}()
	if obj.ErrorPage != "" {
		err := rod.Try(func() {
			page.Timeout(1 * time.Second).MustElement(obj.ErrorPage)

		})

		if err == nil {

			return ArticleContent{
				Title:        obj.Title,
				Link:         obj.Link,
				Date:         obj.Date,
				Content:      "",
				SectionTitle: obj.SectionTitle,
			}, nil

		}

	}

	rod.Try(func() {
		cookieconsent := page.MustElement(obj.FrameConsent)
		switch obj.CookieType {
		case Normal:
			page.MustElement(obj.CookieConsent).MustClick()
		case Frame:
			page.MustElement(obj.FrameConsent).MustFrame().MustElement(obj.CookieConsent).MustClick()
		case None:
			break
		}
		cookieconsent.MustWaitInvisible()

	})
	page.MustSetViewport(1920, 1080, 1, false)

	page.MustWaitIdle()
	text := page.MustElement(obj.ArticleTextSelector).MustText()

	return ArticleContent{
		Title:        obj.Title,
		Link:         obj.Link,
		Date:         obj.Date,
		Content:      text,
		SectionTitle: obj.SectionTitle,
	}, nil
}

func get_article_meta() []ArticleInfo {

	// Create a channel for each link
	channels := make([]chan []ArticleInfo, len(linksToElements))
	for i := range channels {
		channels[i] = make(chan []ArticleInfo)
	}

	// Spawn a goroutine for each channel
	for i, obj := range linksToElements {
		go func(i int, obj ArticleInfoSelectors) {
			articles := processPage(&obj)
			fmt.Printf("processed %d articles\n", len(articles))
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

	return filteredArticles

}

func processPage(obj *ArticleInfoSelectors) []ArticleInfo {

	var articles []ArticleInfo
	path, _ := launcher.LookPath()
	u := launcher.New().Bin(path).MustLaunch()
	println(u)
	browser := rod.New().ControlURL(u).MustConnect()
	page := browser.MustPage()

	err := rod.Try(func() {
		page.Timeout(15 * time.Second).MustNavigate(obj.Link).MustWaitLoad()
	})

	if errors.Is(err, context.DeadlineExceeded) {
		return articles
	}

	rod.Try(func() {
		cookieconsent := page.MustElement(obj.FrameConsent)
		switch obj.CookieType {
		case Normal:
			page.MustElement(obj.CookieConsent).MustClick()
		case Frame:
			page.MustElement(obj.FrameConsent).MustFrame().MustElement(obj.CookieConsent).MustClick()
		case None:
			break
		}
		cookieconsent.MustWaitInvisible()
	})

	page.MustSetViewport(1920, 1080, 1, false)
	defer func() {
		page.MustClose()
		browser.MustClose()
	}()
	page.MustWaitIdle()

	if obj.IsJsPager {
		for i := 1; i <= 2; i++ {
			page.Mouse.MustScroll(0, 1000)
			elem := page.MustElement(obj.Pagination)

			coords := elem.MustShape().Quads[0]
			page.Mouse.MustMoveTo(coords[0]+10, coords[1]+10).MustDown("left")
			page.MustWaitIdle()
		}
		articles = append(articles, getArticles(page, obj)...)
	} else {
		articles = append(articles, getArticles(page, obj)...)
		for i := 0; i <= 1; i++ {
			navs := page.MustElements(obj.Pagination)
			rod.Try(func() {
				href := navs[i].MustElement("a").MustAttribute("href")
				if href != nil {
					page.MustNavigate(*navs[i].MustElement("a").MustAttribute("href"))
				}
				navs[i].MustElement("a").MustClick()

			})
			page.MustWaitNavigation()
			articles = append(articles, getArticles(page, obj)...)
		}
	}

	return articles
}

func getArticles(page *rod.Page, obj *ArticleInfoSelectors) []ArticleInfo {
	var articles []ArticleInfo
	data := page.MustElements(obj.ArticleSelector)
	for _, d := range data {
		title := strings.TrimSpace(d.MustElement(obj.ArticleTitle).MustText())
		link := strings.TrimSpace(*d.MustElement(obj.ArticleLink).MustAttribute("href"))
		dateElem, err := d.Element(obj.ArticleDate)
		var fullLink string
		if strings.HasPrefix(link, "https://") {
			fullLink = link
		} else {
			// slice the link to leave only the path till the third slash
			slicedLink := strings.Split(obj.Link, "/")[:3]
			fullLink = strings.Join(append(slicedLink, link), "/")
		}
		if err == nil {
			dateText := dateElem.MustText()
			articles = append(articles, ArticleInfo{SectionTitle: obj.Title, Title: title, Link: fullLink, Date: strings.TrimSpace(dateText), ArticleTextSelector: obj.ArticleTextSelector, CookieConsent: obj.CookieConsent, FrameConsent: obj.FrameConsent, CookieType: obj.CookieType, ErrorPage: obj.ErrorPage})
		}
	}
	return articles
}
