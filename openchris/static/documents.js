import { action, buttonSubmit, choice, esc, field, header, icon, link, note } from './ui.js?v=carbono-3';

const readable = (doc) => ['ready', 'partial'].includes(doc.reading);
const readingStatus = {
  no_text: 'Sem texto selecionável',
  locked: 'PDF protegido por senha',
  unreadable: 'Texto indisponível',
  unavailable: 'Arquivo indisponível',
};
const units = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 1 });
const size = (bytes) => bytes >= 1024 * 1024
  ? units.format(bytes / (1024 * 1024)) + ' MB'
  : units.format(Math.max(1, bytes / 1024)) + ' KB';

function documentRow(doc, kinds) {
  const id = esc(doc.id);
  const label = esc(doc.name);
  const available = doc.reading !== 'unavailable';
  const details = [kinds[doc.kind], doc.year, size(doc.size)].filter(Boolean).map(esc).join(' · ');
  const permission = readable(doc) ? (doc.chat_enabled ? 'Permitido no chat' : 'Só guardado') : readingStatus[doc.reading];
  return /* HTML */ `<li class="document-row">
    <span class="tool-icon document-icon">${icon('documentos', 19)}</span>
    <div class="document-info">
      <h3>${label}</h3>
      <p class="note">${details}</p>
      <p class="document-status ${doc.chat_enabled ? 'positive' : 'muted'}">${esc(permission)}
        ${doc.reading === 'partial' ? ' · Leitura parcial' : ''}</p>
    </div>
    <div class="document-actions">
      ${readable(doc) ? action('document-toggle', doc.chat_enabled ? 'Pausar no chat' : 'Permitir no chat', 'text-btn',
                                 'data-id="' + id + '" aria-label="' + (doc.chat_enabled ? 'Pausar no chat ' : 'Permitir no chat ') + label + '"') : ''}
      ${available ? '<a class="text-btn" download href="/api/documents/' + encodeURIComponent(doc.id)
        + '/download" aria-label="Baixar ' + label + '">Baixar</a>' : ''}
      ${available ? action('document-delete', 'Excluir', 'text-btn warm', 'data-id="' + id + '" aria-label="Excluir ' + label + '"') : ''}
    </div>
  </li>`;
}

export function documentsView(library) {
  const { items, kinds, available, max_documents: capacity } = library;
  return header('Documentos', '', true) + /* HTML */ `<section class="panel narrow document-library" aria-labelledby="saved-documents">
    <div class="document-heading">
      <div>
        <h2 id="saved-documents" tabindex="-1">Arquivos guardados</h2>
        ${note('Escolha quais arquivos o chat pode consultar para responder às suas perguntas.')}
      </div>
      <div class="actions document-heading-actions">
        ${action('document-upload', 'Adicionar documento', 'primary', available && items.length < capacity ? '' : 'disabled')}
        ${action('finance-chat', 'Abrir chat', 'text-btn')}
      </div>
    </div>
    ${!available ? '<p class="warm" role="status">O armazenamento está indisponível no momento.</p>' : ''}
    ${available && items.length >= capacity ? note('Você chegou ao limite de ' + capacity + ' documentos.') : ''}
    ${items.length ? '<ul class="document-list" role="list">' + items.map(doc => documentRow(doc, kinds)).join('') + '</ul>'
      : '<p class="empty">Guarde uma declaração de IR, um informe de rendimentos ou outro documento de referência.</p>'}
  </section>`;
}

export function uploadDocumentForm({ kinds, max_file_size: maxSize }) {
  return /* HTML */ `<form data-form="document-upload" data-max-size="${esc(maxSize)}">
    <div class="form-grid">
      ${field('file', 'Arquivo', { type: 'file', required: true, attr: 'accept=".pdf,.txt,application/pdf,text/plain"',
        help: 'PDF ou TXT de até ' + size(maxSize) + '. Para o chat ler um PDF, ele precisa ter texto selecionável.' })}
      ${field('name', 'Nome (opcional)', { attr: 'maxlength="200"', help: 'Se deixar vazio, será usado o nome do arquivo.' })}
      ${choice('kind', 'Tipo', kinds, 'other', true)}
      ${field('year', 'Ano dos dados (opcional)', { attr: 'inputmode="numeric" pattern="[0-9]{4}" maxlength="4"' })}
      <div>
        <label class="check"><input type="checkbox" name="chat_enabled"> Permitir consulta pelo chat</label>
        ${note('Quando a pergunta precisar do arquivo, o OpenChris envia trechos à IA para responder.')}
      </div>
    </div>
    ${buttonSubmit('Guardar documento')}
  </form>`;
}

export function deleteDocumentForm(doc) {
  return /* HTML */ `<form data-form="document-delete" data-id="${esc(doc.id)}">
    <p class="dialog-copy">Excluir <strong>${esc(doc.name)}</strong>? O chat deixará de consultar este arquivo. As respostas anteriores ficam no histórico.</p>
    <div class="actions">
      ${action('fechar', 'Cancelar', 'secondary')}
      <button type="submit" class="primary">Excluir documento</button>
    </div>
  </form>`;
}

export function chatDocumentChoices({ items, available }) {
  const docs = items.filter(doc => readable(doc) && doc.chat_enabled);
  const manage = link('documentos', 'Gerenciar documentos');
  if (!available || !docs.length) return manage;
  return choice('document', 'Documentos nesta pergunta', {
    auto: 'Buscar nos documentos permitidos',
    none: 'Não consultar documentos',
    ...Object.fromEntries(docs.map(doc => [doc.id, doc.name + (doc.year ? ' · ' + doc.year : '')])),
  }, 'auto') + manage;
}

export function chatDocumentReferences(sources = []) {
  if (!sources.length) return '';
  return '<p class="note">Documentos citados</p><ul class="chat-references">' + sources.map(source => {
    const detail = [source.name, source.year, source.page ? 'p. ' + source.page : ''].filter(Boolean).map(esc).join(' · ');
    return '<li>' + detail + '</li>';
  }).join('') + '</ul>';
}
