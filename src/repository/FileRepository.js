const Docker = require('dockerode');
const pm2 = require('pm2');
const fs = require('fs-extra');
const path = require('path');

const docker = new Docker({ socketPath: '/var/run/docker.sock' });

class FileRepository {
  /**
   * Helper: exec command di dalam Docker container dan return output
   */
  static async execInContainer(containerId, cmd) {
    const container = docker.getContainer(containerId);
    const exec = await container.exec({
      Cmd: ['sh', '-c', cmd],
      AttachStdout: true,
      AttachStderr: true,
    });

    const stream = await exec.start({ hijack: true, stdin: false });

    return new Promise((resolve, reject) => {
      let stdout = '';
      let stderr = '';

      stream.on('data', (chunk) => {
        const type = chunk[0];
        const data = chunk.slice(8).toString('utf8');
        if (type === 1) stdout += data;
        else if (type === 2) stderr += data;
      });

      stream.on('end', () => {
        if (stderr && !stdout) reject(new Error(stderr.trim()));
        else resolve(stdout);
      });

      stream.on('error', reject);
    });
  }

  /**
   * Deteksi apakah app adalah Docker container atau PM2 process
   * Return: { type: 'docker'|'pm2', cwd, containerId? }
   */
  static async resolveApp(appName) {
    // Cek Docker dulu
    try {
      const containers = await docker.listContainers({ all: true });
      const container = containers.find(c => {
        const name = (c.Names?.[0] || '').replace(/^\//, '');
        return name === appName;
      });

      if (container) {
        const cont = docker.getContainer(container.Id);
        const info = await cont.inspect();
        const cwd = info.Config?.Labels?.['app.cwd'] || '/app';

        return {
          type: 'docker',
          cwd,
          containerId: container.Id,
        };
      }
    } catch (err) {
      // Docker tidak tersedia, lanjut ke PM2
    }

    // Cek PM2
    try {
      await new Promise((resolve, reject) => {
        pm2.connect((err) => {
          if (err) reject(err);
          else resolve();
        });
      });

      const desc = await new Promise((resolve, reject) => {
        pm2.describe(appName, (err, desc) => {
          if (err) reject(err);
          else resolve(desc);
        });
      });

      if (desc && desc.length > 0) {
        return {
          type: 'pm2',
          cwd: desc[0].pm2_env.pm_cwd,
          pm_id: desc[0].pm2_env.pm_id,
        };
      }
    } catch (err) {
      // PM2 tidak tersedia
    } finally {
      try { pm2.disconnect(); } catch (e) { /* ignore */ }
    }

    throw new Error(`Aplikasi '${appName}' tidak ditemukan di Docker maupun PM2`);
  }

  /**
   * List files - support Docker & PM2
   */
  static async listFiles(appName, dir = '') {
    const app = await this.resolveApp(appName);
    const targetPath = path.join(app.cwd, dir);

    if (!targetPath.startsWith(app.cwd)) {
      throw new Error('Akses direktori ditolak');
    }

    if (app.type === 'docker') {
      // Docker: exec ls di container
      const output = await this.execInContainer(
        app.containerId,
        `ls -la "${targetPath}" 2>&1`
      );

      const lines = output.trim().split('\n').filter(l => l && !l.startsWith('total'));
      const files = lines.map(line => {
        const parts = line.split(/\s+/);
        const permissions = parts[0];
        const size = parseInt(parts[4]) || 0;
        const name = parts.slice(8).join(' ');

        if (!name || name === '.' || name === '..') return null;

        return {
          name,
          isDirectory: permissions.startsWith('d'),
          path: path.join(dir, name).replace(/\\/g, '/'),
          size,
        };
      }).filter(Boolean);

      return {
        cwd: app.cwd,
        dir,
        files: files.sort((a, b) => {
          if (a.isDirectory && !b.isDirectory) return -1;
          if (!a.isDirectory && b.isDirectory) return 1;
          return a.name.localeCompare(b.name);
        }),
      };
    } else {
      // PM2: akses filesystem langsung di host
      const items = await fs.readdir(targetPath, { withFileTypes: true });

      const files = await Promise.all(
        items.map(async (item) => {
          const itemPath = path.join(targetPath, item.name);
          let size = 0;
          if (!item.isDirectory()) {
            try {
              const stat = await fs.stat(itemPath);
              size = stat.size;
            } catch {
              size = 0;
            }
          }
          return {
            name: item.name,
            isDirectory: item.isDirectory(),
            path: path.join(dir, item.name).replace(/\\/g, '/'),
            size,
          };
        })
      );

      return {
        cwd: app.cwd,
        dir,
        files: files.sort((a, b) => {
          if (a.isDirectory && !b.isDirectory) return -1;
          if (!a.isDirectory && b.isDirectory) return 1;
          return a.name.localeCompare(b.name);
        }),
      };
    }
  }

  /**
   * Baca file - support Docker & PM2
   */
  static async readFile(appName, filePath) {
    const app = await this.resolveApp(appName);
    const targetPath = path.join(app.cwd, filePath);

    if (!targetPath.startsWith(app.cwd)) {
      throw new Error('Path tidak valid');
    }

    if (app.type === 'docker') {
      const content = await this.execInContainer(app.containerId, `cat "${targetPath}"`);
      return { content };
    } else {
      const content = await fs.readFile(targetPath, 'utf8');
      return { content };
    }
  }

  /**
   * Tulis file - support Docker & PM2
   */
  static async writeFile(appName, filePath, content) {
    const app = await this.resolveApp(appName);
    const targetPath = path.join(app.cwd, filePath);

    if (!targetPath.startsWith(app.cwd)) {
      throw new Error('Path tidak valid');
    }

    if (app.type === 'docker') {
      const escaped = content.replace(/'/g, "'\\''");
      await this.execInContainer(app.containerId, `printf '%s' '${escaped}' > "${targetPath}"`);
    } else {
      await fs.writeFile(targetPath, content, 'utf8');
    }

    return { message: 'File tersimpan' };
  }

  /**
   * Hapus file - support Docker & PM2
   */
  static async deleteFile(appName, filePath) {
    const app = await this.resolveApp(appName);
    const targetPath = path.join(app.cwd, filePath);

    if (!targetPath.startsWith(app.cwd)) {
      throw new Error('Path tidak valid');
    }

    if (app.type === 'docker') {
      await this.execInContainer(app.containerId, `rm -rf "${targetPath}"`);
    } else {
      await fs.remove(targetPath);
    }

    return { message: 'Berhasil dihapus' };
  }

  /**
   * Buat direktori - support Docker & PM2
   */
  static async createDir(appName, dirPath) {
    const app = await this.resolveApp(appName);
    const targetPath = path.join(app.cwd, dirPath);

    if (!targetPath.startsWith(app.cwd)) {
      throw new Error('Path tidak valid');
    }

    if (app.type === 'docker') {
      await this.execInContainer(app.containerId, `mkdir -p "${targetPath}"`);
    } else {
      await fs.ensureDir(targetPath);
    }

    return { message: 'Folder dibuat' };
  }
}

module.exports = FileRepository;
