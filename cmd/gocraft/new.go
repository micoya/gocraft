package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	templateZipURL    = "https://github.com/micoya/gocraft-template/archive/refs/heads/master.zip"
	templateModPath   = "github.com/micoya/gocraft-template"
	templateName      = "gocraft-template"
	templateDirPrefix = "gocraft-template-master"
)

var (
	flagModule   string
	flagSkipTidy bool
	flagNoGit    bool
)

var newCmd = &cobra.Command{
	Use:   "new <project-name>",
	Short: "基于 gocraft 模板创建新项目",
	Long: `从 gocraft 官方模板创建一个新的 Go 项目。

示例:
  gocraft new myapp
  gocraft new myapp --module github.com/myorg/myapp
  gocraft new myapp -m github.com/myorg/myapp --no-git`,
	Args: cobra.ExactArgs(1),
	RunE: runNew,
}

func init() {
	newCmd.Flags().StringVarP(&flagModule, "module", "m", "", "Go module 路径（默认与项目名相同）")
	newCmd.Flags().BoolVar(&flagSkipTidy, "skip-tidy", false, "跳过 go mod tidy")
	newCmd.Flags().BoolVar(&flagNoGit, "no-git", false, "跳过 git init")
	rootCmd.AddCommand(newCmd)
}

func runNew(cmd *cobra.Command, args []string) error {
	projectName := args[0]
	modulePath := flagModule
	if modulePath == "" {
		modulePath = projectName
	}

	if _, err := os.Stat(projectName); err == nil {
		return fmt.Errorf("目录 %q 已存在，请使用其他项目名或删除该目录后重试", projectName)
	}

	fmt.Printf("项目名称: %s\n", projectName)
	fmt.Printf("模块路径: %s\n\n", modulePath)

	fmt.Print("[1/4] 正在下载模板... ")
	zipData, err := downloadWithProgress(templateZipURL)
	if err != nil {
		return fmt.Errorf("下载模板失败: %w", err)
	}
	fmt.Printf("完成 (%d KB)\n", len(zipData)/1024)

	fmt.Print("[2/4] 正在解压并替换包名... ")
	if err := extractAndReplace(zipData, projectName, modulePath); err != nil {
		_ = os.RemoveAll(projectName)
		return fmt.Errorf("初始化项目失败: %w", err)
	}
	fmt.Println("完成")

	if !flagNoGit {
		fmt.Print("[3/4] 正在初始化 Git 仓库... ")
		if err := runGitInit(projectName); err != nil {
			fmt.Printf("跳过（%v）\n", err)
		} else {
			fmt.Println("完成")
		}
	} else {
		fmt.Println("[3/4] 跳过 Git 初始化")
	}

	if !flagSkipTidy {
		fmt.Print("[4/4] 正在执行 go mod tidy... ")
		if err := runGoModTidy(projectName); err != nil {
			fmt.Printf("失败（%v）\n请手动执行: cd %s && go mod tidy\n", err, projectName)
		} else {
			fmt.Println("完成")
		}
	} else {
		fmt.Println("[4/4] 跳过 go mod tidy")
	}

	fmt.Printf("\n项目 %q 创建成功！\n", projectName)
	fmt.Printf("  cd %s\n", projectName)
	fmt.Println("  go run ./cmd/apiserver")
	return nil
}

func downloadWithProgress(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func extractAndReplace(zipData []byte, projectName, modulePath string) error {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("解析 zip 失败: %w", err)
	}

	prefix := templateDirPrefix + "/"

	for _, f := range r.File {
		if err := extractFile(f, prefix, projectName, modulePath); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, prefix, projectName, modulePath string) error {
	// 剥离压缩包顶层目录
	rel := strings.TrimPrefix(f.Name, prefix)
	if rel == f.Name || rel == "" {
		return nil
	}

	// 替换路径中的模板名
	rel = strings.ReplaceAll(rel, templateDirPrefix, projectName)

	destPath := filepath.Join(projectName, rel)

	// 路径穿越防护
	absProject, _ := filepath.Abs(projectName)
	absDest, _ := filepath.Abs(destPath)
	if !strings.HasPrefix(absDest, absProject+string(os.PathSeparator)) {
		return fmt.Errorf("检测到非法路径: %s", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(destPath, 0o755)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return err
	}

	if isTextFile(f.Name) {
		// 先替换完整模块路径，再替换裸模板名称（Makefile/Dockerfile/配置文件等）
		content = bytes.ReplaceAll(content, []byte(templateModPath), []byte(modulePath))
		content = bytes.ReplaceAll(content, []byte(templateName), []byte(projectName))
	}

	return os.WriteFile(destPath, content, f.Mode())
}

// isTextFile 判断是否为需要做包名替换的文本文件。
func isTextFile(name string) bool {
	base := filepath.Base(name)
	// 无扩展名的已知文本文件
	switch base {
	case "Makefile", "Dockerfile", ".env", ".gitignore", ".gitattributes":
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".go", ".mod", ".sum", ".yaml", ".yml", ".toml", ".json",
		".md", ".txt", ".sh", ".env", ".conf", ".ini", ".xml", ".html", ".proto":
		return true
	}
	return false
}

func runGitInit(dir string) error {
	c := exec.Command("git", "init")
	c.Dir = dir
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	return c.Run()
}

func runGoModTidy(dir string) error {
	c := exec.Command("go", "mod", "tidy")
	c.Dir = dir
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	return c.Run()
}
