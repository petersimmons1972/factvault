package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

type lessonEntry struct {
	ID           string `json:"id"`
	Subject      string `json:"subject"`
	Body         string `json:"body"`
	CreatedAt    string `json:"created_at"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

func newLessonsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lessons",
		Short: "Manage local protocol lessons",
	}
	cmd.AddCommand(newLessonsListCmd(), newLessonsGetCmd(), newLessonsProposeCmd())
	return cmd
}

func newLessonsListCmd() *cobra.Command {
	var includeSuperseded bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List lessons",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			entries, err := loadLessons(root)
			if err != nil {
				return err
			}
			out := make([]lessonEntry, 0, len(entries))
			for _, entry := range entries {
				if entry.SupersededBy != "" && !includeSuperseded {
					continue
				}
				out = append(out, entry)
			}
			return json.NewEncoder(os.Stdout).Encode(out)
		},
	}
	cmd.Flags().BoolVar(&includeSuperseded, "include-superseded", false, "include superseded lessons")
	return cmd
}

func newLessonsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get SUBJECT",
		Short: "Get lessons for a subject",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			entries, err := loadLessons(root)
			if err != nil {
				return err
			}
			out := make([]lessonEntry, 0)
			for _, entry := range entries {
				if entry.Subject == args[0] {
					out = append(out, entry)
				}
			}
			return json.NewEncoder(os.Stdout).Encode(out)
		},
	}
	return cmd
}

func newLessonsProposeCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "propose SUBJECT",
		Short: "Record a proposed lesson",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := cmd.Flags().GetString("root")
			entries, err := loadLessons(root)
			if err != nil {
				return err
			}
			if body == "" {
				return fmt.Errorf("lesson body is required")
			}
			entry := lessonEntry{
				ID:        fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
				Subject:   args[0],
				Body:      body,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			entries = append(entries, entry)
			if err := saveLessons(root, entries); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(entry)
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "lesson body [required]")
	_ = cmd.MarkFlagRequired("body")
	return cmd
}

func lessonsPath(root string) string {
	return filepath.Join(root, "lessons.json")
}

func loadLessons(root string) ([]lessonEntry, error) {
	return readJSONFile(lessonsPath(root), []lessonEntry{})
}

func saveLessons(root string, entries []lessonEntry) error {
	return writeJSONFile(lessonsPath(root), entries)
}
