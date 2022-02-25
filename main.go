package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/dghubble/go-twitter/twitter"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	_ "github.com/go-sql-driver/mysql"
)

type Config struct {
	DiscordConfig  DiscordConfig  `json:"discordConf"`
	TwitterConfig  TwitterConfig  `json:"twitterConf"`
	DatabaseConfig DatabaseConfig `json:"databaseConf"`
	Params         Params         `json:"params"`
}

type DiscordConfig struct {
	Token string `json:"token"`
}
type TwitterConfig struct {
	ConsumerKey    string `json:"consumerKey"`
	ConsumerSecret string `json:"consumerSecret"`
	AccessToken    string `json:"accessToken"`
	AccessSecret   string `json:"accessSecret"`
}
type DatabaseConfig struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
}
type Params struct {
	Subjects []string `json:"subjects"`
	Users    []string `json:"users"`
	Channels []string `json:"channels"`
	Interval int      `json:"interval"`
}

func loadConfig() {
	file, err := os.Open("config.json")
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}

var config Config
var client *twitter.Client
var dg *discordgo.Session

func main() {
	loadConfig()

	fmt.Printf("loggin twitter client with %v and %v\n", config.TwitterConfig.ConsumerKey, config.TwitterConfig.ConsumerSecret)

	// oauth2 configures a client that uses app credentials to keep a fresh token
	configTwitter := &clientcredentials.Config{
		ClientID:     config.TwitterConfig.ConsumerKey,
		ClientSecret: config.TwitterConfig.ConsumerSecret,
		TokenURL:     "https://api.twitter.com/oauth2/token",
	}
	// http.Client will automatically authorize Requests
	httpClient := configTwitter.Client(oauth2.NoContext)

	// Twitter client
	client = twitter.NewClient(httpClient)

	// Create a new Discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + config.DiscordConfig.Token)
	if err != nil {
		fmt.Println("Error creating Discord session: ", err)
		return
	}

	// Register ready as a callback for the ready events.
	dg.AddHandler(ready)

	// Register messageCreate as a callback for the messageCreate events.
	dg.AddHandler(messageCreate)

	// We need information about guilds (which includes their channels),
	// messages and voice states.
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages

	// Open the websocket and begin listening.
	err = dg.Open()
	if err != nil {
		fmt.Println("Error opening Discord session: ", err)
	}

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("News is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Cleanly close down the Discord session.
	dg.Close()
}

// This function will be called (due to AddHandler above) when the bot receives
// the "ready" event from Discord.
func ready(s *discordgo.Session, event *discordgo.Ready) {
	db, err := sql.Open("mysql", config.DatabaseConfig.User+":"+config.DatabaseConfig.Password+"@tcp("+config.DatabaseConfig.Host+":"+config.DatabaseConfig.Port+")/"+config.DatabaseConfig.Database)
	if err != nil {
		fmt.Println("Error connecting database:", err)
	}
	// Set the playing status.
	s.UpdateGameStatus(0, "dev env")
	// If a news anchor isnt in the database at launch create him a row
	for _, i := range config.Params.Users {
		results, _ := db.Query("SELECT * FROM users WHERE username = ?", i)

		defer results.Close()
		if !results.Next() {
			_, err := db.Exec("INSERT INTO users (username) VALUES (?)", i)
			if err != nil {
				panic(
					fmt.Sprintf("Error inserting user: %v", err),
				)
			}
		}
	}
	db.Close()
	// launch the news loop
	go operateNews(s)
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}
	if strings.HasPrefix(m.Content, "!news") {
		compte := m.Content[6:]
		if compte != "" {
			tweet := getTweets(compte)

			s.ChannelMessageSend(m.ChannelID, tweet.FullText)

		}

	}
}

func makeEmbed(tweet *twitter.Tweet) *discordgo.MessageEmbed {

	footerText := fmt.Sprintf("%v   |   %v  🔄   |   %v  ❤", tweet.CreatedAt, tweet.RetweetCount, tweet.FavoriteCount)
	var embed *discordgo.MessageEmbed
	if len(tweet.Entities.Media) > 0 {

		embed = &discordgo.MessageEmbed{
			Title: tweet.User.Name,
			Author: &discordgo.MessageEmbedAuthor{
				Name:    tweet.User.Name + " (@" + tweet.User.ScreenName + ")",
				URL:     "https://twitter.com/" + tweet.User.ScreenName + "/status/" + tweet.IDStr,
				IconURL: tweet.User.ProfileImageURL,
			},
			Image: &discordgo.MessageEmbedImage{
				URL: tweet.Entities.Media[0].MediaURLHttps,
			},
			Description: tweet.Text,
			Color:       0xff0000,

			Footer: &discordgo.MessageEmbedFooter{
				Text: footerText + "   |   média   |   @ping",
				//IconURL: "img.icons8.com/cute-clipart/50/000000/twitter.png",
			},
		}

	} else {
		embed = &discordgo.MessageEmbed{
			Title: tweet.User.Name,
			Author: &discordgo.MessageEmbedAuthor{
				Name:    tweet.User.Name + " (@" + tweet.User.ScreenName + ")",
				URL:     "https://twitter.com/" + tweet.User.ScreenName + "/status/" + tweet.IDStr,
				IconURL: tweet.User.ProfileImageURL,
			},

			Description: tweet.Text,
			Color:       0xff0000,

			Footer: &discordgo.MessageEmbedFooter{
				Text: footerText + "  |   @ping",
				//IconURL: "img.icons8.com/cute-clipart/10/000000/twitter.png",
			},
		}

		return embed
	}
	return nil
}
func getTweets(compte string) *twitter.Tweet {
	tweets, _, err := client.Timelines.UserTimeline(&twitter.UserTimelineParams{
		ScreenName: compte,
		Count:      1,
	})
	if err != nil {
		panic("error getting tweets")
	}
	tweetId, _ := strconv.ParseInt(tweets[0].IDStr, 10, 64)
	tweet, _, err := client.Statuses.Show(tweetId, nil)
	if err != nil {
		panic("error getting tweet")
	}
	return tweet
}

type User struct {
	Username    string `json:"username"`
	LastTweetId string `json:"lastTweetId"`
}

func operateNews(s *discordgo.Session) {
	for {
		db, err := sql.Open("mysql", config.DatabaseConfig.User+":"+config.DatabaseConfig.Password+"@tcp("+config.DatabaseConfig.Host+":"+config.DatabaseConfig.Port+")/"+config.DatabaseConfig.Database)
		if err != nil {
			fmt.Println("Error connecting database:", err)
		}
		for _, i := range config.Params.Users {
			fmt.Println("Getting tweets for user:", i)

			results, err := db.Query("SELECT username,lastTweetId FROM users WHERE username = ?", i)
			if err != nil {
				fmt.Println("Error:", err)
				return
			}
			defer results.Close()
			var user User
			for results.Next() {
				err = results.Scan(&user.Username, &user.LastTweetId)
				if err != nil {
					fmt.Println("Error parsing sql response :", err)
					return
				}
			}
			tweet := getTweets(i)
			if user.LastTweetId == tweet.IDStr {
				fmt.Printf("Last tweet id for %v : %v\n", i, tweet.IDStr)
			} else {
				db.Query("UPDATE users SET lastTweetId = ? WHERE username = ?", tweet.IDStr, i)
				embed := makeEmbed(tweet)
				fmt.Printf("channels : %v ", config.Params.Channels)
				for _, j := range config.Params.Channels {
					fmt.Println("Sending tweet to channel:", j)
					_, err := s.ChannelMessageSendEmbed(j, embed)
					if err != nil {
						fmt.Println("Error sending tweet:", err)
					}
				}
			}
		}
		fmt.Printf("Sleeping for : %v seconds\n", config.Params.Interval)
		time.Sleep(time.Duration(config.Params.Interval) * time.Second)
		db.Close()
	}
}
