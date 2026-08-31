// Command provider-credentials updates third-party API keys used by
// vision-service. It is an administration utility, not a deployable service
// or a request-serving process.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/bootstrap"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/database"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "配置 Provider API Key 失败：", err)
		os.Exit(1)
	}
}

func run() error {
	var confPath string
	var provider string
	flag.StringVar(&confPath, "conf", "./configs/vision.yaml", "vision config file")
	flag.StringVar(&provider, "provider", "all", "credential to update: all, bailian, or deepseek")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "all" && provider != "bailian" && provider != "deepseek" {
		return fmt.Errorf("provider must be all, bailian, or deepseek")
	}

	bc, err := bootstrap.Load(confPath)
	if err != nil {
		return err
	}
	db, err := database.OpenPostgres(bc.GetDatabase())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := database.Close(db); closeErr != nil {
			fmt.Fprintln(os.Stderr, "关闭数据库失败：", closeErr)
		}
	}()
	schemaContext, cancelSchemaMigration := context.WithTimeout(context.Background(), time.Minute)
	err = data.AutoMigrateSchema(schemaContext, db)
	cancelSchemaMigration()
	if err != nil {
		return fmt.Errorf("初始化 Vision 数据库表结构：%w", err)
	}
	repo, err := data.NewProviderCredentialRepo(db)
	if err != nil {
		return err
	}

	updated := 0
	if provider == "all" || provider == "bailian" {
		apiKey, err := readPassword("请输入百炼 API Key（直接回车表示不修改）：")
		if err != nil {
			return err
		}
		if apiKey != "" {
			if err := setCredential(repo, biz.ProviderCredentialNameBailianParaformer, apiKey); err != nil {
				return err
			}
			updated++
			fmt.Fprintln(os.Stderr, "百炼 API Key 已保存到数据库。")
		}
	}
	if provider == "all" || provider == "deepseek" {
		apiKey, err := readPassword("请输入 DeepSeek API Key（直接回车表示不修改）：")
		if err != nil {
			return err
		}
		if apiKey != "" {
			if err := setCredential(repo, biz.ProviderCredentialNameDeepSeek, apiKey); err != nil {
				return err
			}
			updated++
			fmt.Fprintln(os.Stderr, "DeepSeek API Key 已保存到数据库。")
		}
	}
	if updated == 0 {
		return fmt.Errorf("没有输入任何 API Key，数据库未修改")
	}
	return nil
}

func readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("必须在交互式终端中输入 API Key")
	}
	fmt.Fprint(os.Stderr, prompt)
	value, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("读取 API Key: %w", err)
	}
	return string(value), nil
}

func setCredential(repo *data.ProviderCredentialRepo, provider biz.ProviderCredentialName, apiKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	credential, err := repo.SetProviderCredential(ctx, provider, apiKey)
	if err != nil {
		return err
	}
	if credential == nil || credential.Version <= 0 {
		return fmt.Errorf("Provider API Key 保存结果无效")
	}
	return nil
}
