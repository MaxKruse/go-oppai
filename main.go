package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/schema"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	oppai "github.com/flesnuk/oppai5"
)

var (
	downloadPath string = "/data/beatmaps"
	API_KEY      string = os.Getenv("OSU_API_KEY")
	WORKING_DIR  string
	decoder      *schema.Decoder = schema.NewDecoder()

	dsn = fmt.Sprintf("host=%v user=%v password=%v dbname=%v port=%v sslmode=disable TimeZone=Europe/Berlin", os.Getenv("POSTGRES_HOST"), os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD"), os.Getenv("POSTGRES_DB"), os.Getenv("POSTGRES_PORT"))
	db  *gorm.DB

	err error
)

type OppaiRequest struct {
	Beatmap     string  `json:"md5"`
	Mods        uint32  `json:"mods,omitempty"`
	Accuracy    float32 `json:"accuracy,omitempty"`
	Combo       int32   `json:"combo,omitempty"`
	FullPoints  int32   `json:"300,omitempty"`
	ThirdPoints int32   `json:"100,omitempty"`
	SixthPoints int32   `json:"50,omitempty"`
	Miss        int32   `json:"Miss,omitempty"`
}

type GenerateOppai struct {
	Beatmap     string  `json:"md5"`
	Mods        uint32  `json:"mods,omitempty"`
	Accuracy    float32 `json:"accuracy,omitempty"`
	Combo       int32   `json:"combo,omitempty"`
	FullPoints  int32   `json:"300,omitempty"`
	ThirdPoints int32   `json:"100,omitempty"`
	SixthPoints int32   `json:"50,omitempty"`
	Miss        int32   `json:"Miss,omitempty"`
}

type PerformanceResponse struct {
	gorm.Model
	MapMD5 string  `json:"beatmap_md5" ,gorm:"primaryKey"`
	PP     float64 `json:"performance_points"`
}

type ErrorResponse struct {
	Code    int32  `json:"error_code"`
	Message string `json:"message"`
}

func getOppai(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Credentials", "true")
	// Get Beatmap Request
	var req OppaiRequest

	// URL params to OppaiRequest
	err := decoder.Decode(&req, r.URL.Query())

	// if we cant decode, tell error
	if err != nil || len(req.Beatmap) < 1 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{400, fmt.Sprintf("Malformed request body: %s - Expected at least 'beatmap=<hash>'", r.URL.Query())})
		return
	}

	// Check Database for beatmap hash
	var res PerformanceResponse

	count := int64(0)
	db.Model(&PerformanceResponse{}).Where("map_md5 = ?", req.Beatmap).Count(&count)

	if count > 0 {
		db.First(&res, "map_md5 = ?", req.Beatmap)
		// We have the hash, just return
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(res)
		return
	}

	// We dont have a hash, return error
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(ErrorResponse{404, fmt.Sprintf("Beatmap not found: %s", req.Beatmap)})
}

// DownloadFile will download a url to a local file. It's efficient because it will
// write as it downloads and not load the whole file into memory.
func DownloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

func get_osuapi(endpoint string, query string) (result []map[string]interface{}, err error) {
	req := fmt.Sprintf("https://osu.ppy.sh/api%v?k=%v&%v", endpoint, API_KEY, query)
	res, err := http.Get(req)

	if err != nil {
		return nil, err
	}
	json.NewDecoder(res.Body).Decode(&result)

	return result, err
}

func generateOppai(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Access-Control-Allow-Credentials", "true")

	// Get Beatmap + Mods as string
	var result GenerateOppai

	// URL params to OppaiRequest
	err := json.NewDecoder(r.Body).Decode(&result)

	if err != nil || len(result.Beatmap) < 1 {
		w.WriteHeader(http.StatusBadRequest)
		log.Fatalln(err)
		json.NewEncoder(w).Encode(ErrorResponse{400, fmt.Sprintf("Malformed request body: %s - Expected at least 'beatmap=<hash>'", r.Body)})
		return
	}

	// Check if we even have to generate anything, 7 days since last is good enough
	count := int64(0)
	err = db.Model(&PerformanceResponse{}).Where("map_md5 = ?", result.Beatmap).Count(&count).Error

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Fatalln(err)
		json.NewEncoder(w).Encode(ErrorResponse{400, "Some database error, no idea..."})
		return
	}

	// download beatmap file if not exist
	beatmapPath := fmt.Sprintf("%s\\%v.osu", downloadPath, result.Beatmap)
	// Download beatmapPath is the hash, not beatmapid
	beatmapPath = strings.ReplaceAll(beatmapPath, "\\", "/")

	if _, err = os.Stat(beatmapPath); os.IsNotExist(err) {
		// Get Beatmap ID from checksum
		// See: https://github.com/ppy/osu-api/wiki
		beatmapResponse, err := get_osuapi("/get_beatmaps", fmt.Sprintf("h=%v", result.Beatmap))

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			log.Fatalln(err)
			json.NewEncoder(w).Encode(ErrorResponse{400, fmt.Sprintf("Could not get API response for beatmap hash: %v", r.Body)})
			return
		}

		beatmapID := beatmapResponse[0]["beatmap_id"].(string)
		url := fmt.Sprintf("https://osu.ppy.sh/osu/%v", beatmapID)

		err = DownloadFile(beatmapPath, url)

		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			log.Fatalln(err)
			json.NewEncoder(w).Encode(ErrorResponse{500, fmt.Sprintf("Could not download beatmap (MD5 %v) (ID %v)", result.Beatmap, beatmapID)})
			return
		}
	}

	// run Oppai against it, save result
	f, err := ioutil.ReadFile(beatmapPath)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		log.Fatalln(err)
		json.NewEncoder(w).Encode(ErrorResponse{500, fmt.Sprintf("Could not open beatmap (MD5 %v)", result.Beatmap)})
		return
	}
	beatmap := oppai.Parse(bytes.NewReader(f))

	// Add DB Entry for hash, mods, etc
	var resp PerformanceResponse
	resp.MapMD5 = result.Beatmap
	resp.PP = oppai.PPInfo(beatmap, nil).PP.Total

	if count > 0 {
		var temp PerformanceResponse
		db.First(&temp, "map_md5 = ?", result.Beatmap)

		then := temp.UpdatedAt
		now := time.Now()

		diff := int(now.Sub(then).Hours())
		var target int = 24 * 7

		if temp.ID > 0 && diff-target >= 0 {
			// No need
			json.NewEncoder(w).Encode(&temp)
			return
		} else {
			temp.PP = resp.PP
			db.Save(&temp)
			json.NewEncoder(w).Encode(&temp)
			return
		}
	}

	db.Save(&resp)
	// options := &oppai.Parameters{Combo: uint16(beatmap.MaxCombo), N300: uint16(beatmap.NCircles + beatmap.NSliders + beatmap.NSpinners), N100: 0, N50: 0, Misses: 0, Mods: 0}
	json.NewEncoder(w).Encode(&resp)
}

func main() {
	WORKING_DIR, err = os.Getwd()

	// check osu api key
	if len(API_KEY) < 16 {
		log.Fatal("OSU_API_KEY not set, exiting...")
		os.Exit(1)
	}

	// connect to db
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})

	if err != nil {
		log.Fatalf("Could not connect to postgres database at %v, exiting...", dsn)
		os.Exit(1)
	}

	// Migrate db
	db.AutoMigrate(&PerformanceResponse{})

	// Easy access from terminal output
	PORT := ":5000"
	URL := fmt.Sprintf("http://localhost%s", PORT)

	// Generate new Mux Router and routes
	r := mux.NewRouter()

	r.HandleFunc("/pp", getOppai).Methods("GET")
	r.HandleFunc("/pp", generateOppai).Methods("POST")

	// Print32all Routes for easy terminal access
	log.Println("All routes:")

	r.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		temp, err := route.GetPathTemplate()
		met, err2 := route.GetMethods()
		if err != nil || err2 != nil {
			return err
		}

		for _, m := range met {
			log.Printf("[%s] %s%s", strings.ToUpper(m), URL, temp)
		}
		return nil
	})

	// Setup the http server with security meassures
	var wait time.Duration
	srv := &http.Server{
		Addr: fmt.Sprintf("0.0.0.0%s", PORT),
		// Good practice to set timeouts to avoid Slowloris attacks.
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r, // Pass our instance of gorilla/mux in.
	}

	// Run our server in a goroutine so that it doesn't block.
	go func() {
		log.Printf("Serving on %s", URL)
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalln(err)
		}
	}()

	c := make(chan os.Signal, 1)
	// We'll accept graceful shutdowns when quit via SIGint32(Ctrl+C)
	// SIGKILL, SIGQUIT or SIGTERM (Ctrl+/) will not be caught.
	signal.Notify(c, os.Interrupt)

	// Block until we receive our signal.
	<-c

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	// Doesn't block if no connections, but will otherwise wait
	// until the timeout deadline.
	srv.Shutdown(ctx)

	log.Println("Shutting down")
	os.Exit(0)
}