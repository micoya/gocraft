package main

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "gocraft",
	Short: "gocraft CLI - Go 项目脚手架工具",
	Long: `gocraft 是基于 gocraft 框架的 CLI 工具。
提供项目创建、代码生成等功能，帮助 PHP 团队快速落地 Go 开发。`,
}
