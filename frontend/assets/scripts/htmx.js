// HTMX 4 setup for server-side component architecture.
import '../styles/tailwind.css'
import '../styles/components.css'
import htmx from 'htmx.org'

window.htmx = htmx

// HTMX configuration for component-based rendering
document.addEventListener('DOMContentLoaded', function() {
    htmx.config.history = true
    
    // Configure HTMX for component rendering
    htmx.config.requestClass = 'htmx-request'
    
    // Global HTMX event listeners for component interactions
    document.body.addEventListener('htmx:before:request', function(event) {
        console.log('HTMX request to:', event.detail.ctx?.request?.action)
        
        // Add loading state to target element
        const target = event.detail.ctx?.target || document.body
        if (target) {
            target.classList.add('loading')
        }
    })
    
    document.body.addEventListener('htmx:after:request', function(event) {
        console.log('HTMX response received, status:', event.detail.ctx?.response?.status)
        
        // Remove loading state
        const target = event.detail.ctx?.target
        if (target) {
            target.classList.remove('loading')
        }
    })
    
    // Component-specific event handling
    document.body.addEventListener('htmx:after:swap', function(event) {
        // Re-initialize any client-side component behaviors
        initializeComponentBehaviors(event.detail.ctx?.target)
    })
    
    // Error handling for component requests
    document.body.addEventListener('htmx:response:error', function(event) {
        console.error('HTMX error:', event.detail.ctx?.response?.status)
        
        // Show user-friendly error message
        const target = event.detail.ctx?.target
        if (target) {
            target.innerHTML = `
                <div class="error-message">
                    <h3>Something went wrong</h3>
                    <p>Please try refreshing the page</p>
                    <button onclick="location.reload()">Refresh</button>
                </div>
            `
        }
    })
})

// Initialize interactive behaviors for components
function initializeComponentBehaviors(element) {
    // Example: Initialize tooltips, modals, etc.
    const interactiveElements = element.querySelectorAll('[data-behavior]')
    interactiveElements.forEach(el => {
        const behavior = el.dataset.behavior
        switch(behavior) {
            case 'tooltip':
                initializeTooltip(el)
                break
            case 'modal':
                initializeModal(el)
                break
            case 'dropdown':
                initializeDropdown(el)
                break
            case 'tabs':
                initializeTabs(el)
                break
        }
    })
    
    // Initialize any component-specific JavaScript
    const componentElements = element.querySelectorAll('[data-component]')
    componentElements.forEach(el => {
        const componentType = el.dataset.component
        initializeComponent(el, componentType)
    })
}

function initializeTooltip(element) {
    element.addEventListener('mouseenter', function() {
        const tooltip = this.getAttribute('data-tooltip')
        if (tooltip) {
            showTooltip(this, tooltip)
        }
    })
    
    element.addEventListener('mouseleave', function() {
        hideTooltip()
    })
}

function initializeModal(element) {
    element.addEventListener('click', function() {
        const modalId = this.getAttribute('data-modal-target')
        if (modalId) {
            openModal(modalId)
        }
    })
}

function initializeDropdown(element) {
    const trigger = element.querySelector('[data-dropdown-trigger]')
    const menu = element.querySelector('[data-dropdown-menu]')
    
    if (trigger && menu) {
        trigger.addEventListener('click', function() {
            menu.classList.toggle('show')
        })
        
        // Close on outside click
        document.addEventListener('click', function(e) {
            if (!element.contains(e.target)) {
                menu.classList.remove('show')
            }
        })
    }
}

function initializeTabs(element) {
    const tabs = element.querySelectorAll('[data-tab]')
    const panels = element.querySelectorAll('[data-tab-panel]')
    
    tabs.forEach(tab => {
        tab.addEventListener('click', function() {
            const targetPanel = this.getAttribute('data-tab')
            
            // Remove active class from all tabs and panels
            tabs.forEach(t => t.classList.remove('active'))
            panels.forEach(p => p.classList.remove('active'))
            
            // Add active class to clicked tab and corresponding panel
            this.classList.add('active')
            const panel = element.querySelector(`[data-tab-panel="${targetPanel}"]`)
            if (panel) {
                panel.classList.add('active')
            }
        })
    })
}

function initializeComponent(element, type) {
    switch(type) {
        case 'user-card':
            initializeUserCard(element)
            break
        case 'data-table':
            initializeDataTable(element)
            break
        case 'form':
            initializeForm(element)
            break
    }
}

function initializeUserCard(element) {
    // Add interactive behaviors for user cards
    const button = element.querySelector('button')
    if (button) {
        button.addEventListener('click', function() {
            // Add loading state
            this.textContent = 'Loading...'
            this.disabled = true
        })
    }
}

function initializeDataTable(element) {
    // Add sorting, filtering, etc.
    const headers = element.querySelectorAll('th[data-sortable]')
    headers.forEach(header => {
        header.addEventListener('click', function() {
            const column = this.getAttribute('data-sortable')
            sortTable(element, column)
        })
    })
}

function initializeForm(element) {
    // Add form validation and submission handling
    element.addEventListener('submit', function(e) {
        // Add any client-side validation here
        const submitBtn = this.querySelector('button[type="submit"]')
        if (submitBtn) {
            submitBtn.textContent = 'Submitting...'
            submitBtn.disabled = true
        }
    })
}

// Utility functions
function showTooltip(element, text) {
    const tooltip = document.createElement('div')
    tooltip.className = 'tooltip'
    tooltip.textContent = text
    tooltip.style.position = 'absolute'
    tooltip.style.backgroundColor = '#333'
    tooltip.style.color = 'white'
    tooltip.style.padding = '5px 10px'
    tooltip.style.borderRadius = '4px'
    tooltip.style.fontSize = '12px'
    tooltip.style.zIndex = '1000'
    
    const rect = element.getBoundingClientRect()
    tooltip.style.top = (rect.top - 30) + 'px'
    tooltip.style.left = rect.left + 'px'
    
    document.body.appendChild(tooltip)
}

function hideTooltip() {
    const tooltip = document.querySelector('.tooltip')
    if (tooltip) {
        tooltip.remove()
    }
}

function openModal(modalId) {
    const modal = document.getElementById(modalId)
    if (modal) {
        modal.style.display = 'block'
        modal.classList.add('show')
    }
}

function sortTable(table, column) {
    // Simple table sorting implementation
    console.log('Sorting table by column:', column)
}
