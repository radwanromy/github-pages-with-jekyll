package main
import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"flag"
	"mime"
	"time"
	"strconv"
	"strings"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v2"
)

var (
	inputPath = flag.String("i","","input file path")
	outputFile = flag.String("o","","output filename")
	folderName = flag.String("f","","folder name")
)
func getClient(ctx context.Context, config *oauth2.Config) *http.Client {
	cacheFile, err := tokenCacheFile()
	if err != nil {
		log.Fatalf("Unable to get path to cached credential file. %v", err)
	}
	tok, err := tokenFromFile(cacheFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(cacheFile, tok)
	}
	return config.Client(ctx, tok)
}
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
	"authorization code: \n%v\n", authURL)
	fmt.Printf("Enter Verfication Code:\n")

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		log.Fatalf("Unable to read authorization code %v", err)
	}

	tok, err := config.Exchange(oauth2.NoContext, code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web %v", err)
	}
	return tok
}
func tokenCacheFile() (string, error) {
	usr, err := user.Current()
	if err != nil {
		// return "", err
		return usr.HomeDir, err
	}
	tokenCacheDir := ".credentials"
	os.MkdirAll(tokenCacheDir, 0700)
	return filepath.Join(tokenCacheDir,
		url.QueryEscape("drive-api-cert.json")), err
}
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	t := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(t)
	defer f.Close()
	return t, err
}

// saveToken uses a file path to create a file and store the
// token in it.
func saveToken(file string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", file)
	f, err := os.Create(file)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

func Comma(v int64) string {
	sign := ""
	if v < 0 {
		sign = "-"
		v = 0 - v
	}

	parts := []string{"", "", "", "", "", "", ""}
	j := len(parts) - 1

	for v > 999 {
		parts[j] = strconv.FormatInt(v%1000, 10)
		switch len(parts[j]) {
		case 2:
			parts[j] = "0" + parts[j]
		case 1:
			parts[j] = "00" + parts[j]
		}
		v = v / 1000
		j--
	}
	parts[j] = strconv.Itoa(int(v))
	return sign + strings.Join(parts[j:], ",")
}

func FileSizeFormat(bytes int64, forceBytes bool) string {
	if forceBytes {
		return fmt.Sprintf("%v B", bytes)
	}

	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}

	var i int
	value := float64(bytes)

	for value > 1000 {
		value /= 1000
		i++
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}

func MeasureTransferRate() func(int64) string {
	start := time.Now()

	return func(bytes int64) string {
		seconds := int64(time.Now().Sub(start).Seconds())
		if seconds < 1 {
			return fmt.Sprintf("%s/s", FileSizeFormat(bytes, false))
		}
		bps := bytes / seconds
		return fmt.Sprintf("%s/s", FileSizeFormat(bps, false))
	}
}

func getOrCreateFolder(d *drive.Service, folderName string) string{
	folderId := ""
	if folderName == "" {
		return ""
	}
	q := fmt.Sprintf("title=\"%s\" and mimeType=\"application/vnd.google-apps.folder\"", folderName)

	r, err := d.Files.List().Q(q).MaxResults(1).Do()
	if err != nil {
		log.Fatalf("Unable to retrieve foldername.", err)
	}

	if len(r.Items) > 0 {
		folderId = r.Items[0].Id
	} else {
		fmt.Printf("Folder not found. Create new folder : %s\n", folderName)
		f := &drive.File{Title: folderName, Description: "Auto Create by gdrive-upload", MimeType: "application/vnd.google-apps.folder"}
		r, err := d.Files.Insert(f).Do()
		if err != nil {
			fmt.Printf("An error occurred when create folder: %v\n", err)
		}
		folderId = r.Id
	}
	return folderId
}

func uploadFile(d *drive.Service, title string, description string,
parentName string, mimeType string, filename string) (*drive.File, error) {
	input, err := os.Open(filename)
	if err != nil {
		fmt.Printf("An error occurred: %v\n", err)
		return nil, err
	}
	inputInfo, err := input.Stat()
	if err != nil {
		return nil, err
	}

	parentId := getOrCreateFolder(d, parentName)

	fmt.Println("Start upload")
	f := &drive.File{Title: title, Description: description, MimeType: mimeType}
	if parentId != "" {
		p := &drive.ParentReference{Id: parentId}
		f.Parents = []*drive.ParentReference{p}
	}
	getRate := MeasureTransferRate()

	// progress call back
	showProgress := func(current, total int64){
		fmt.Printf("Uploaded at %s, %s/%s\r" , getRate(current), Comma(current), Comma(total))
	}

	r, err := d.Files.Insert(f).ResumableMedia(context.Background(), input, inputInfo.Size(),mimeType).ProgressUpdater(showProgress).Do()
	if err != nil {
		fmt.Printf("An error occurred: %v\n", err)
		return nil, err
	}

	bytes := r.FileSize
	fmt.Printf("Uploaded '%s' at %s, total %s\n", r.Title, getRate(bytes), FileSizeFormat(bytes, false))
	fmt.Printf("Upload Done. ID : %s\n", r.Id)
	return r, nil
}


func main() {
	flag.Parse()
  ctx := context.Background()

	b, err := ioutil.ReadFile("client_secret.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	config, err := google.ConfigFromJSON(b, drive.DriveScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(ctx, config)

	srv, err := drive.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve drive Client %v", err)
	}

	fmt.Printf("Read file: %s\n", *inputPath)
	outputTitle := *outputFile
	if outputTitle == "" {
		outputTitle = filepath.Base(*inputPath)
	}
	fmt.Printf("Output name: %s\n", outputTitle)

	ext := filepath.Ext( *inputPath)
	mimeType := "application/octet-stream"
	if ext != ""{
		mimeType = mime.TypeByExtension(ext)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	fmt.Printf("Mime : %s\n", mimeType)

	uploadFile(srv, outputTitle, "", *folderName, mimeType, *inputPath)
}
