// This file is part of arduino-cli.
//
// Copyright 2020 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-cli.
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

package builder

import (
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/arduino/arduino-cli/arduino/sketch"
	"github.com/arduino/arduino-cli/i18n"
	"github.com/arduino/arduino-cli/legacy/builder/builder_utils"
	"github.com/arduino/arduino-cli/legacy/builder/phases"
	"github.com/arduino/arduino-cli/legacy/builder/types"
	"github.com/arduino/arduino-cli/legacy/builder/utils"
	"github.com/pkg/errors"
)

var tr = i18n.Tr

var MAIN_FILE_VALID_EXTENSIONS = map[string]bool{".ino": true, ".pde": true}
var ADDITIONAL_FILE_VALID_EXTENSIONS = map[string]bool{".h": true, ".c": true, ".hpp": true, ".hh": true, ".cpp": true, ".S": true}
var ADDITIONAL_FILE_VALID_EXTENSIONS_NO_HEADERS = map[string]bool{".c": true, ".cpp": true, ".S": true}

const DEFAULT_DEBUG_LEVEL = 5
const DEFAULT_WARNINGS_LEVEL = "none"
const DEFAULT_SOFTWARE = "ARDUINO"

type Builder struct{}

func (s *Builder) Run(ctx *types.Context) error {
	if err := ctx.BuildPath.MkdirAll(); err != nil {
		return err
	}

	commands := []types.Command{
		&ContainerSetupHardwareToolsLibsSketchAndProps{},

		&ContainerBuildOptions{},

		&WarnAboutPlatformRewrites{},

		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.prebuild", Suffix: ".pattern"},

		&ContainerMergeCopySketchFiles{},

		utils.LogIfVerbose("info", tr("Detecting libraries used...")),
		&ContainerFindIncludes{},

		&WarnAboutArchIncompatibleLibraries{},

		utils.LogIfVerbose("info", tr("Generating function prototypes...")),
		&PreprocessSketch{},

		utils.LogIfVerbose("info", tr("Compiling sketch...")),
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.sketch.prebuild", Suffix: ".pattern"},
		&phases.SketchBuilder{},
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.sketch.postbuild", Suffix: ".pattern", SkipIfOnlyUpdatingCompilationDatabase: true},

		utils.LogIfVerbose("info", tr("Compiling libraries...")),
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.libraries.prebuild", Suffix: ".pattern"},
		&UnusedCompiledLibrariesRemover{},
		&phases.LibrariesBuilder{},
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.libraries.postbuild", Suffix: ".pattern", SkipIfOnlyUpdatingCompilationDatabase: true},

		utils.LogIfVerbose("info", tr("Compiling core...")),
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.core.prebuild", Suffix: ".pattern"},
		&phases.CoreBuilder{},
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.core.postbuild", Suffix: ".pattern", SkipIfOnlyUpdatingCompilationDatabase: true},

		utils.LogIfVerbose("info", tr("Linking everything together...")),
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.linking.prelink", Suffix: ".pattern"},
		&phases.Linker{},
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.linking.postlink", Suffix: ".pattern", SkipIfOnlyUpdatingCompilationDatabase: true},

		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.objcopy.preobjcopy", Suffix: ".pattern"},
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.objcopy.", Suffix: ".pattern", SkipIfOnlyUpdatingCompilationDatabase: true},
		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.objcopy.postobjcopy", Suffix: ".pattern", SkipIfOnlyUpdatingCompilationDatabase: true},

		&MergeSketchWithBootloader{},

		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.postbuild", Suffix: ".pattern", SkipIfOnlyUpdatingCompilationDatabase: true},
	}

	mainErr := runCommands(ctx, commands)

	if ctx.CompilationDatabase != nil {
		ctx.CompilationDatabase.SaveToFile()
	}

	commands = []types.Command{
		&PrintUsedAndNotUsedLibraries{SketchError: mainErr != nil},

		&PrintUsedLibrariesIfVerbose{},

		&ExportProjectCMake{SketchError: mainErr != nil},

		&phases.Sizer{SketchError: mainErr != nil},
	}
	otherErr := runCommands(ctx, commands)

	if mainErr != nil {
		return mainErr
	}

	return otherErr
}

type PreprocessSketch struct{}

func (s *PreprocessSketch) Run(ctx *types.Context) error {
	var commands []types.Command
	if ctx.UseArduinoPreprocessor {
		commands = append(commands, &PreprocessSketchArduino{})
	} else {
		commands = append(commands, &ContainerAddPrototypes{})
	}
	return runCommands(ctx, commands)
}

type Preprocess struct{}

func (s *Preprocess) Run(ctx *types.Context) error {
	if ctx.BuildPath == nil {
		ctx.BuildPath = sketch.GenBuildPath(ctx.SketchLocation)
	}

	if err := ctx.BuildPath.MkdirAll(); err != nil {
		return err
	}

	commands := []types.Command{
		&ContainerSetupHardwareToolsLibsSketchAndProps{},

		&ContainerBuildOptions{},

		&RecipeByPrefixSuffixRunner{Prefix: "recipe.hooks.prebuild", Suffix: ".pattern"},

		&ContainerMergeCopySketchFiles{},

		&ContainerFindIncludes{},

		&WarnAboutArchIncompatibleLibraries{},

		&PreprocessSketch{},
	}

	if err := runCommands(ctx, commands); err != nil {
		return err
	}

	// Output arduino-preprocessed source
	ctx.ExecStdout.Write([]byte(ctx.Source))
	return nil
}

type ParseHardwareAndDumpBuildProperties struct{}

func (s *ParseHardwareAndDumpBuildProperties) Run(ctx *types.Context) error {
	if ctx.BuildPath == nil {
		ctx.BuildPath = sketch.GenBuildPath(ctx.SketchLocation)
	}

	commands := []types.Command{
		&ContainerSetupHardwareToolsLibsSketchAndProps{},

		&DumpBuildProperties{},
	}

	return runCommands(ctx, commands)
}

func runCommands(ctx *types.Context, commands []types.Command) error {
	ctx.Progress.AddSubSteps(len(commands))
	defer ctx.Progress.RemoveSubSteps()

	for _, command := range commands {
		PrintRingNameIfDebug(ctx, command)
		err := command.Run(ctx)
		if err != nil {
			return errors.WithStack(err)
		}
		ctx.Progress.CompleteStep()
		builder_utils.PrintProgressIfProgressEnabledAndMachineLogger(ctx)
	}
	return nil
}

func PrintRingNameIfDebug(ctx *types.Context, command types.Command) {
	if ctx.DebugLevel >= 10 {
		ctx.GetLogger().Fprintln(os.Stdout, "debug", "Ts: {0} - Running: {1}", strconv.FormatInt(time.Now().Unix(), 10), reflect.Indirect(reflect.ValueOf(command)).Type().Name())
	}
}

func RunBuilder(ctx *types.Context) error {
	command := Builder{}
	return command.Run(ctx)
}

func RunParseHardwareAndDumpBuildProperties(ctx *types.Context) error {
	command := ParseHardwareAndDumpBuildProperties{}
	return command.Run(ctx)
}

func RunPreprocess(ctx *types.Context) error {
	command := Preprocess{}
	return command.Run(ctx)
}
