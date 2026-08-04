const fs = require('node:fs')
const path = require('node:path')
const https = require('node:https')

const version = 'v4.42-9798-rtm-2023.06.30'
const fileName = `softether-vpnclient-${version}-windows-x86_x64-intel.exe`
const url = `https://www.softether-download.com/files/softether/${version}-tree/Windows/SoftEther_VPN_Client/${fileName}`
const outputDir = path.join(__dirname, '..', 'build')
const outputPath = path.join(outputDir, fileName)

function download(target, redirects = 0) {
  return new Promise((resolve, reject) => {
    https.get(target, (response) => {
      if ([301, 302, 303, 307, 308].includes(response.statusCode) && response.headers.location) {
        response.resume()
        if (redirects >= 5) return reject(new Error('SoftEther 下载重定向次数过多'))
        return resolve(download(new URL(response.headers.location, target).toString(), redirects + 1))
      }
      if (response.statusCode !== 200) {
        response.resume()
        return reject(new Error(`SoftEther 下载失败，HTTP ${response.statusCode}`))
      }
      fs.mkdirSync(outputDir, { recursive: true })
      const file = fs.createWriteStream(outputPath)
      response.pipe(file)
      file.on('finish', () => file.close(resolve))
      file.on('error', reject)
    }).on('error', reject)
  })
}

if (fs.existsSync(outputPath) && fs.statSync(outputPath).size > 1024 * 1024) {
  console.log(`Using cached SoftEther installer: ${fileName}`)
} else {
  console.log(`Downloading SoftEther VPN Client ${version}`)
  download(url).then(() => {
    console.log(`Saved SoftEther installer: ${outputPath}`)
  }).catch((error) => {
    fs.rmSync(outputPath, { force: true })
    console.error(error.message)
    process.exitCode = 1
  })
}
