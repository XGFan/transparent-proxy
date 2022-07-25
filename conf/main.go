package main

import (
	"embed"
	"github.com/goccy/go-yaml"
	"github.com/labstack/echo/v4"
	"golang.org/x/sys/execabs"
	"io/fs"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
)

type Action struct {
	Pre  string `json:"pre,omitempty"`
	Post string `json:"post,omitempty"`
}
type ConfItem struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
	FileType    string `json:"fileType,omitempty"`
	Action      Action `json:"action"`
}

type ConfigList []ConfItem

func (cl ConfigList) FindByName(name string) *ConfItem {
	for _, item := range cl {
		if item.Name == name {
			return &item
		}
	}
	return nil
}

func (conf *ConfItem) Save(content []byte) error {
	return ioutil.WriteFile(conf.Location, content, 0644)
}

func (conf *ConfItem) PostExec() ([]byte, error) {
	if conf.Action.Post == "" {
		return nil, nil
	}
	split := strings.Split(conf.Action.Post, " ")
	command := execabs.Command(split[0], split[1:]...)
	return command.CombinedOutput()
}

func initData() ConfigList {
	file, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		log.Fatal(err)
	}
	items := make([]ConfItem, 0)
	err = yaml.Unmarshal(file, &items)
	if err != nil {
		log.Fatal(err)
	}
	for i, item := range items {
		sub := strings.Split(item.Location, ".")
		if len(sub) != 0 {
			ext := sub[len(sub)-1]
			item.FileType = ext
			items[i] = item
		}
	}
	cl := ConfigList(items)
	return cl
}

//go:embed dist
var dist embed.FS

func getFileSystem(useOS bool) http.FileSystem {
	if useOS {
		log.Print("using live mode")
		return http.FS(os.DirFS("dist"))
	}
	log.Print("using embed mode")
	fsys, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return http.FS(fsys)
}

type FsAdapter struct {
	forward embed.FS
}

func (f FsAdapter) Open(name string) (fs.File, error) {
	if name == "." {
		return f.forward.Open("dist")
	}
	open, err := f.forward.Open("dist/" + name)
	return open, err
}

func main() {
	configDatas := initData()
	e := echo.New()
	useOS := len(os.Args) > 1 && os.Args[1] == "live"
	//serverFs := http.FileServer(http.FS(FsAdapter{dist}))

	assetHandler := http.FileServer(getFileSystem(useOS))
	e.GET("/*", echo.WrapHandler(assetHandler))
	e.GET("/api/conf", func(c echo.Context) error {
		return c.JSON(http.StatusOK, configDatas)
	})
	e.GET("/api/conf/:name/content", func(c echo.Context) error {
		name := c.Param("name")
		item := configDatas.FindByName(name)
		if item != nil {
			return c.String(http.StatusOK, itemContent(item).Content)
		}
		return c.NoContent(http.StatusNotFound)
	})
	e.POST("/api/conf/:name/content", func(c echo.Context) error {
		name := c.Param("name")
		item := configDatas.FindByName(name)
		if item != nil {
			all, err := ioutil.ReadAll(c.Request().Body)
			if err == nil {
				err = item.Save(all)
				if err == nil {
					var result []byte
					result, err = item.PostExec()
					if err == nil {
						return c.Blob(http.StatusOK, "text/plain", result)
					}
				}
			}
			c.Error(err)
		}
		return c.NoContent(http.StatusNotFound)
	})
	e.Logger.Fatal(e.Start(":1323"))
}

type ConfItemView struct {
	ConfItem
	Content string
}

func itemContent(item *ConfItem) *ConfItemView {
	file, _ := ioutil.ReadFile(item.Location)
	return &ConfItemView{
		*item,
		string(file),
	}
}
