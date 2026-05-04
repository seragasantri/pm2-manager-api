const TokenService = require('../service/TokenService');
const AppResponse = require('../http/response/AppResponse');

class TokenController {
  static async index(req, res) {
    try {
      const tokens = await TokenService.getAllTokens();
      return AppResponse.success(res, tokens);
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async store(req, res) {
    try {
      const { label, allowedApps } = req.body;

      if (!label) {
        return AppResponse.validationError(res, { label: 'Label diperlukan' });
      }

      if (!allowedApps || allowedApps.length === 0) {
        return AppResponse.validationError(res, { allowedApps: 'Minimal 1 aplikasi harus dipilih' });
      }

      const token = await TokenService.createToken({ label, allowedApps }, req.user.id);
      return AppResponse.created(res, token, 'Token berhasil dibuat');
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async update(req, res) {
    try {
      const { id } = req.params;
      const { label, allowedApps } = req.body;

      const token = await TokenService.updateToken(id, { label, allowedApps });
      return AppResponse.success(res, {
        ...token,
        allowed_apps: typeof token.allowed_apps === 'string' ? JSON.parse(token.allowed_apps) : token.allowed_apps,
      }, 'Token berhasil diupdate');
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async destroy(req, res) {
    try {
      const { id } = req.params;
      await TokenService.deleteToken(id);
      return AppResponse.success(res, null, 'Token berhasil dihapus');
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }
}

module.exports = TokenController;