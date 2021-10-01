package commands

import (
	"encoding/xml"
	"strconv"
	"strings"
	"time"

	"github.com/inconshreveable/log15"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	c "github.com/vulsio/goval-dictionary/config"
	"github.com/vulsio/goval-dictionary/db"
	"github.com/vulsio/goval-dictionary/fetcher"
	"github.com/vulsio/goval-dictionary/models"
	"github.com/vulsio/goval-dictionary/util"
	"github.com/ymomoi/goval-parser/oval"
	"golang.org/x/xerrors"
)

// fetchRedHatCmd is Subcommand for fetch RedHat OVAL
var fetchRedHatCmd = &cobra.Command{
	Use:   "redhat",
	Short: "Fetch Vulnerability dictionary from RedHat",
	Long:  `Fetch Vulnerability dictionary from RedHat`,
	RunE:  fetchRedHat,
}

func init() {
	fetchCmd.AddCommand(fetchRedHatCmd)
}

func fetchRedHat(cmd *cobra.Command, args []string) (err error) {
	if err := util.SetLogger(viper.GetBool("log-to-file"), viper.GetString("log-dir"), viper.GetBool("debug"), viper.GetBool("log-json")); err != nil {
		return xerrors.Errorf("Failed to SetLogger. err: %w", err)
	}

	if len(args) == 0 {
		return xerrors.New("Failed to fetch redhat command. err: specify versions to fetch")
	}

	driver, locked, err := db.NewDB(viper.GetString("dbtype"), viper.GetString("dbpath"), viper.GetBool("debug-sql"))
	if err != nil {
		if locked {
			return xerrors.Errorf("Failed to open DB. Close DB connection before fetching. err: %w", err)
		}
		return err
	}

	fetchMeta, err := driver.GetFetchMeta()
	if err != nil {
		return xerrors.Errorf("Failed to get FetchMeta from DB. err: %w", err)
	}
	if fetchMeta.OutDated() {
		return xerrors.Errorf("Failed to Insert CVEs into DB. SchemaVersion is old. SchemaVersion: %+v", map[string]uint{"latest": models.LatestSchemaVersion, "DB": fetchMeta.SchemaVersion})
	}

	if err := driver.UpsertFetchMeta(fetchMeta); err != nil {
		return xerrors.Errorf("Failed to upsert FetchMeta to DB. err: %w", err)
	}

	// Distinct
	vers := []string{}
	v := map[string]bool{}
	for _, arg := range args {
		ver, err := strconv.Atoi(arg)
		if err != nil || ver < 5 {
			return xerrors.Errorf("Specify version to fetch (from 5 to latest RHEL version). arg: %s", arg)
		}
		v[arg] = true
	}
	for k := range v {
		vers = append(vers, k)
	}

	results, err := fetcher.FetchRedHatFiles(vers)
	if err != nil {
		return xerrors.Errorf("Failed to fetch files. err: %w", err)
	}

	for _, r := range results {
		ovalroot := oval.Root{}
		if err = xml.Unmarshal(r.Body, &ovalroot); err != nil {
			return xerrors.Errorf("Failed to unmarshal. url: %s, err: %w", r.URL, err)
		}
		log15.Info("Fetched", "URL", r.URL, "OVAL definitions", len(ovalroot.Definitions.Definitions))
		defs := models.ConvertRedHatToModel(&ovalroot)

		var timeformat = "2006-01-02T15:04:05"
		t, err := time.Parse(timeformat, ovalroot.Generator.Timestamp)
		if err != nil {
			return xerrors.Errorf("Failed to parse time. err: %w", err)
		}

		root := models.Root{
			Family:      c.RedHat,
			OSVersion:   r.Target,
			Definitions: defs,
			Timestamp:   time.Now(),
		}

		ss := strings.Split(r.URL, "/")
		fmeta := models.FileMeta{
			Timestamp: t,
			FileName:  ss[len(ss)-1],
		}

		if err := driver.InsertOval(&root, fmeta); err != nil {
			return xerrors.Errorf("Failed to insert oval. err: %w", err)
		}
		if err := driver.InsertFileMeta(fmeta); err != nil {
			return xerrors.Errorf("Failed to insert meta. err: %w", err)
		}
		log15.Info("Finish", "Updated", len(root.Definitions))
	}

	return nil
}
