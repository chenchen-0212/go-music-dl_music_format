go env -w GOPROXY=https://goproxy.cn,direct
echo Generate Windows resources from the selected ICO
go generate ./winres
cd desktop_go

echo Build Go desktop app
go mod tidy
go build -ldflags="-H windowsgui -w -s" -o ..\music-dl-desktop-go.exe

del *.syso
cd ..
echo Build complete!
echo You can now run music-dl-desktop-go.exe
pause
