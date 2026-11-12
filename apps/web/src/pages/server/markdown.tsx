import type { ComponentProps } from 'react'
import ReactMarkdown from 'react-markdown'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize from 'rehype-sanitize'
import remarkGfm from 'remark-gfm'

// The Markdown pipeline (parser, HTML parser and sanitizer) is most of what
// the mods tab would otherwise download, and it is only needed once someone
// opens a description or a changelog. It lives here so it can load then; use
// the Markdown component in mod-browse, which does that.

const MD: ComponentProps<typeof ReactMarkdown>['components'] = {
  a: ({ href, children }) => (
    <a className="link" href={href} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  ),
  img: ({ src, alt }) => <img src={typeof src === 'string' ? src : undefined} alt={alt ?? ''} loading="lazy" referrerPolicy="no-referrer" />,
}

/** A project's own text: Markdown, often with HTML mixed in, which is kept
 *  to what GitHub would allow (no scripts, styles or event handlers). */
export default function MarkdownText({ text }: { text: string }) {
  return (
    <div className="md">
      <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeRaw, rehypeSanitize]} components={MD}>
        {text}
      </ReactMarkdown>
    </div>
  )
}
